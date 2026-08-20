package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandPromoteBacklog = "PromoteBacklog"

const backlogPromotionMigration = `
CREATE TABLE backlog_promotions (
    backlog_handle TEXT PRIMARY KEY,
    operation_id TEXT NOT NULL UNIQUE,
    subject_digest TEXT NOT NULL,
    task_operation_id TEXT NOT NULL UNIQUE,
    task_handle TEXT NOT NULL DEFAULT '',
    reserved_at TEXT NOT NULL,
    completed_at TEXT NOT NULL DEFAULT '',
    FOREIGN KEY(backlog_handle) REFERENCES backlog_items(handle)
);
CREATE INDEX backlog_promotions_operation_idx
ON backlog_promotions(operation_id, backlog_handle);
INSERT INTO schema_migrations(version, applied_at)
VALUES (38, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

var _ application.BacklogPromotionStore = (*Store)(nil)

type backlogPromotionRow struct {
	backlogHandle, operationID, subjectDigest string
	taskOperationID, taskHandle               string
	reservedAt                                time.Time
	completedAt                               *time.Time
}

// ReplayBacklogPromotion returns one completed item-to-task promotion.
func (store *Store) ReplayBacklogPromotion(
	ctx context.Context,
	operationID, subjectDigest string,
) (application.BacklogPromotionResult, bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.BacklogPromotionResult{}, false, fmt.Errorf("begin backlog promotion replay: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(ctx, transaction, operationID, commandPromoteBacklog, subjectDigest)
	if err != nil {
		return application.BacklogPromotionResult{}, false, commitReplayConflict(transaction, err)
	}
	if !found {
		return application.BacklogPromotionResult{}, false, nil
	}
	result, err := readBacklogPromotionResult(ctx, transaction, operation)
	if err != nil {
		return application.BacklogPromotionResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.BacklogPromotionResult{}, false, fmt.Errorf("commit backlog promotion replay: %w", err)
	}
	return result, true, nil
}

// ReserveBacklogPromotion durably excludes every other operation before task
// preparation may allocate a worktree.
func (store *Store) ReserveBacklogPromotion(
	ctx context.Context,
	reservation application.BacklogPromotionReservation,
) (application.BacklogPromotionReservation, error) {
	if err := validateBacklogPromotionReservationInput(reservation); err != nil {
		return application.BacklogPromotionReservation{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.BacklogPromotionReservation{}, fmt.Errorf("begin backlog promotion reservation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	existing, found, err := readBacklogPromotionRow(ctx, transaction, reservation.OperationID)
	if err != nil {
		return application.BacklogPromotionReservation{}, err
	}
	if found {
		if existing.backlogHandle != reservation.BacklogHandle || existing.subjectDigest != reservation.SubjectDigest ||
			existing.taskOperationID != reservation.TaskOperationID {
			return application.BacklogPromotionReservation{}, fmt.Errorf("backlog promotion reservation altered replay: %w", application.ErrConflict)
		}
		return completeBacklogPromotionReservation(ctx, transaction, existing)
	}
	item, satisfied, err := promotableBacklogItem(ctx, transaction, reservation.BacklogHandle)
	if err != nil {
		return application.BacklogPromotionReservation{}, err
	}
	const insert = `INSERT INTO backlog_promotions(
        backlog_handle, operation_id, subject_digest, task_operation_id, reserved_at
    ) VALUES (?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		reservation.BacklogHandle, reservation.OperationID, reservation.SubjectDigest,
		reservation.TaskOperationID, formatTime(reservation.ReservedAt),
	); err != nil {
		if isConstraintError(err) {
			return application.BacklogPromotionReservation{}, fmt.Errorf("reserve backlog promotion: %w", application.ErrConflict)
		}
		return application.BacklogPromotionReservation{}, fmt.Errorf("reserve backlog promotion: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.BacklogPromotionReservation{}, fmt.Errorf("commit backlog promotion reservation: %w", err)
	}
	reservation.Item = item
	reservation.SatisfiedDependencies = satisfied
	return reservation, nil
}

// CommitBacklogPromotion binds the reserved item to an exact durable child
// preparation and only then moves the item to promoted.
func (store *Store) CommitBacklogPromotion(
	ctx context.Context,
	mutation application.BacklogPromotionMutation,
) (application.BacklogPromotionResult, error) {
	if err := validateBacklogPromotionMutation(mutation); err != nil {
		return application.BacklogPromotionResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("begin backlog promotion: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if operation, found, err := mutationReplay(
		ctx, transaction, mutation.Reservation.OperationID,
		commandPromoteBacklog, mutation.Reservation.SubjectDigest,
	); err != nil {
		return application.BacklogPromotionResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return readBacklogPromotionResult(ctx, transaction, operation)
	}
	row, found, err := readBacklogPromotionRow(ctx, transaction, mutation.Reservation.OperationID)
	if err != nil {
		return application.BacklogPromotionResult{}, err
	}
	if !found || row.backlogHandle != mutation.Reservation.BacklogHandle ||
		row.subjectDigest != mutation.Reservation.SubjectDigest ||
		row.taskOperationID != mutation.Reservation.TaskOperationID || row.completedAt != nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("backlog promotion reservation is unavailable: %w", application.ErrPrecondition)
	}
	item, satisfied, err := promotableBacklogItem(ctx, transaction, row.backlogHandle)
	if err != nil {
		return application.BacklogPromotionResult{}, err
	}
	if !reflect.DeepEqual(item, mutation.Reservation.Item) ||
		!reflect.DeepEqual(satisfied, mutation.Reservation.SatisfiedDependencies) {
		return application.BacklogPromotionResult{}, fmt.Errorf("backlog promotion reservation changed: %w", application.ErrPrecondition)
	}
	childOperation, err := getOperation(ctx, transaction, row.taskOperationID)
	if err != nil || childOperation.Command != commandPrepareTask ||
		childOperation.Status != domain.OperationCompleted || childOperation.ResultRef == "" {
		return application.BacklogPromotionResult{}, fmt.Errorf("backlog task preparation is unavailable: %w", application.ErrPrecondition)
	}
	prepared, err := mutationResult(ctx, transaction, childOperation)
	if err != nil {
		return application.BacklogPromotionResult{}, err
	}
	if !reflect.DeepEqual(prepared, mutation.Prepared) || prepared.Task.RepositoryID != item.RepositoryID ||
		prepared.Task.Shape != item.Shape || prepared.Preparation == nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("backlog task preparation changed: %w", application.ErrPrecondition)
	}
	updated := item
	updated.Readiness, updated.UpdatedAt = domain.BacklogPromoted, mutation.At
	result, err := transaction.ExecContext(ctx,
		"UPDATE backlog_items SET readiness = ?, updated_at = ? WHERE handle = ? AND readiness = ?",
		updated.Readiness, formatTime(updated.UpdatedAt), updated.Handle, domain.BacklogReady,
	)
	if err != nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("update promoted backlog item: %w", err)
	}
	if changed, err := result.RowsAffected(); err != nil || changed != 1 {
		return application.BacklogPromotionResult{}, fmt.Errorf("update promoted backlog item: %w", application.ErrPrecondition)
	}
	linkResult, err := transaction.ExecContext(ctx, `UPDATE backlog_promotions
        SET task_handle = ?, completed_at = ?
        WHERE operation_id = ? AND task_handle = '' AND completed_at = ''`,
		prepared.Task.Handle, formatTime(mutation.At), row.operationID,
	)
	if err != nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("complete backlog promotion link: %w", err)
	}
	if changed, err := linkResult.RowsAffected(); err != nil || changed != 1 {
		return application.BacklogPromotionResult{}, fmt.Errorf("complete backlog promotion link: %w", application.ErrPrecondition)
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.BacklogPromotionResult{}, err
	}
	operation := completedMutationOperation(
		row.operationID, commandPromoteBacklog, row.subjectDigest,
		prepared.Task.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("insert backlog promotion operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.BacklogPromotionResult{}, fmt.Errorf("commit backlog promotion: %w", err)
	}
	return application.BacklogPromotionResult{
		Item: updated, Task: prepared.Task, Preparation: prepared.Preparation, Operation: operation,
	}, nil
}

func readBacklogPromotionResult(
	ctx context.Context,
	source queryer,
	operation domain.OperationRecord,
) (application.BacklogPromotionResult, error) {
	row, found, err := readBacklogPromotionRow(ctx, source, operation.ID)
	if err != nil || !found || row.completedAt == nil || row.taskHandle == "" || row.taskHandle != operation.ResultRef ||
		row.subjectDigest != operation.SubjectDigest {
		return application.BacklogPromotionResult{}, errors.New("backlog promotion result is incomplete")
	}
	item, err := getBacklogItem(ctx, source, row.backlogHandle)
	if err != nil || item.Readiness != domain.BacklogPromoted {
		return application.BacklogPromotionResult{}, errors.New("promoted backlog item is unavailable")
	}
	childOperation, err := getOperation(ctx, source, row.taskOperationID)
	if err != nil || childOperation.Command != commandPrepareTask || childOperation.ResultRef != row.taskHandle {
		return application.BacklogPromotionResult{}, errors.New("promoted backlog task operation is unavailable")
	}
	prepared, err := mutationResult(ctx, source, childOperation)
	if err != nil || prepared.Preparation == nil {
		return application.BacklogPromotionResult{}, errors.New("promoted backlog task preparation is unavailable")
	}
	return application.BacklogPromotionResult{
		Item: item, Task: prepared.Task, Preparation: prepared.Preparation, Operation: operation,
	}, nil
}

func completeBacklogPromotionReservation(
	ctx context.Context,
	transaction *sql.Tx,
	row backlogPromotionRow,
) (application.BacklogPromotionReservation, error) {
	item, satisfied, err := promotableBacklogItem(ctx, transaction, row.backlogHandle)
	if err != nil {
		return application.BacklogPromotionReservation{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.BacklogPromotionReservation{}, fmt.Errorf("commit backlog promotion reservation replay: %w", err)
	}
	return application.BacklogPromotionReservation{
		OperationID: row.operationID, SubjectDigest: row.subjectDigest,
		BacklogHandle: row.backlogHandle, TaskOperationID: row.taskOperationID,
		ReservedAt: row.reservedAt, Item: item, SatisfiedDependencies: satisfied,
	}, nil
}

func promotableBacklogItem(
	ctx context.Context,
	source queryer,
	handle string,
) (domain.BacklogItem, []string, error) {
	item, err := getBacklogItem(ctx, source, handle)
	if err != nil {
		return domain.BacklogItem{}, nil, err
	}
	satisfaction := make(map[string]bool, len(item.DependsOn))
	satisfied := make([]string, 0, len(item.DependsOn))
	for _, dependency := range item.DependsOn {
		dependencyItem, err := getBacklogItem(ctx, source, dependency)
		if err != nil || dependencyItem.Readiness != domain.BacklogPromoted {
			return domain.BacklogItem{}, nil, fmt.Errorf("backlog dependency is not promoted: %w", application.ErrPrecondition)
		}
		satisfaction[dependency], satisfied = true, append(satisfied, dependency)
	}
	if err := item.CheckPromotable(satisfaction); err != nil {
		return domain.BacklogItem{}, nil, fmt.Errorf("backlog item is not promotable: %w", application.ErrPrecondition)
	}
	return item, satisfied, nil
}

func readBacklogPromotionRow(
	ctx context.Context,
	source queryer,
	operationID string,
) (backlogPromotionRow, bool, error) {
	const query = `SELECT backlog_handle, operation_id, subject_digest, task_operation_id,
        task_handle, reserved_at, completed_at
        FROM backlog_promotions WHERE operation_id = ?`
	var row backlogPromotionRow
	var reservedAt, completedAt string
	err := source.QueryRowContext(ctx, query, operationID).Scan(
		&row.backlogHandle, &row.operationID, &row.subjectDigest, &row.taskOperationID,
		&row.taskHandle, &reservedAt, &completedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return backlogPromotionRow{}, false, nil
	}
	if err != nil {
		return backlogPromotionRow{}, false, fmt.Errorf("read backlog promotion reservation: %w", err)
	}
	row.reservedAt, err = parseTime(reservedAt)
	if err != nil {
		return backlogPromotionRow{}, false, errors.New("backlog promotion reservation time is invalid")
	}
	if completedAt != "" {
		parsed, parseErr := parseTime(completedAt)
		if parseErr != nil {
			return backlogPromotionRow{}, false, errors.New("backlog promotion completion time is invalid")
		}
		row.completedAt = &parsed
	}
	return row, true, nil
}

func validateBacklogPromotionReservationInput(reservation application.BacklogPromotionReservation) error {
	if domain.ValidateOperationID(reservation.OperationID) != nil || len(reservation.SubjectDigest) != 64 ||
		domain.ValidateTaskHandle(reservation.BacklogHandle) != nil ||
		domain.ValidateOperationID(reservation.TaskOperationID) != nil ||
		reservation.ReservedAt.Location() != time.UTC || reservation.Item.Handle != "" ||
		len(reservation.SatisfiedDependencies) != 0 {
		return errors.New("backlog promotion reservation is invalid")
	}
	return nil
}

func validateBacklogPromotionMutation(mutation application.BacklogPromotionMutation) error {
	reservation := mutation.Reservation
	if domain.ValidateOperationID(reservation.OperationID) != nil || len(reservation.SubjectDigest) != 64 ||
		domain.ValidateTaskHandle(reservation.BacklogHandle) != nil ||
		domain.ValidateOperationID(reservation.TaskOperationID) != nil || reservation.Item.Validate() != nil ||
		reservation.Item.Handle != reservation.BacklogHandle || reservation.ReservedAt.Location() != time.UTC ||
		mutation.At.Location() != time.UTC || mutation.At.Before(reservation.ReservedAt) ||
		mutation.Prepared.Operation.ID != reservation.TaskOperationID || mutation.Prepared.Task.Handle == "" ||
		mutation.Prepared.Preparation == nil {
		return errors.New("backlog promotion mutation is invalid")
	}
	return nil
}
