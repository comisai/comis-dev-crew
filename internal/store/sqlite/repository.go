package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const sqliteConstraintCode = 19

// CreateTask inserts one validated task record. The service mutation
// coordinator is the only production caller allowed to invoke it.
func (store *Store) CreateTask(ctx context.Context, task domain.Task) error {
	if err := insertTask(ctx, store.db, task); err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

type execer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func insertTask(ctx context.Context, target execer, task domain.Task) error {
	if err := task.Validate(); err != nil {
		return err
	}
	acceptanceCriteria, err := json.Marshal(task.AcceptanceCriteria)
	if err != nil {
		return fmt.Errorf("encode task acceptance criteria: %w", err)
	}
	constraints, err := json.Marshal(task.Constraints)
	if err != nil {
		return fmt.Errorf("encode task constraints: %w", err)
	}
	const statement = `INSERT INTO tasks (
        handle, schema_version, service_instance_id, managed_run_id,
        workspace_lease_id, state, shape, repository_id, base_revision,
        brief_revision, brief_revision_hash, acceptance_criteria_json,
        constraints_json, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = target.ExecContext(ctx, statement,
		task.Handle,
		task.SchemaVersion,
		task.ServiceInstanceID,
		task.ManagedRunID,
		task.WorkspaceLeaseID,
		task.State,
		task.Shape,
		task.RepositoryID,
		task.BaseRevision,
		task.BriefRevision,
		task.BriefRevisionHash,
		string(acceptanceCriteria),
		string(constraints),
		task.ValidationProfile,
		task.DeliveryMode,
		task.WorkerProfileID,
		task.ReportCursor,
		task.StateVersion,
		formatTime(task.CreatedAt),
		formatTime(task.UpdatedAt),
	)
	if err != nil {
		if isConstraintError(err) {
			return application.ErrConflict
		}
		return err
	}
	return nil
}

// RecordOperation inserts one immutable outcome. Repeating its stable ID with
// the same command and subject digest reuses that logical effect; altered
// content is a conflict. Callers read the record to recover the original outcome.
func (store *Store) RecordOperation(ctx context.Context, operation domain.OperationRecord) error {
	err := insertOperation(ctx, store.db, operation)
	if err != nil {
		if isConstraintError(err) {
			existing, readErr := store.GetOperation(ctx, operation.ID)
			if readErr != nil {
				return fmt.Errorf("resolve operation replay: %w", readErr)
			}
			if existing.Command == operation.Command && existing.SubjectDigest == operation.SubjectDigest {
				return nil
			}
			return fmt.Errorf("record operation altered replay: %w", application.ErrConflict)
		}
		return fmt.Errorf("record operation: %w", err)
	}
	return nil
}

func insertOperation(ctx context.Context, target execer, operation domain.OperationRecord) error {
	if err := operation.Validate(); err != nil {
		return err
	}
	const statement = `INSERT INTO operations (
        id, schema_version, command, subject_digest, status, error_code,
        result_ref, state_version, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement,
		operation.ID, operation.SchemaVersion, operation.Command,
		operation.SubjectDigest, operation.Status, operation.ErrorCode,
		operation.ResultRef, operation.StateVersion,
		formatTime(operation.CreatedAt), formatTime(operation.UpdatedAt),
	)
	return err
}

// ListTasks returns a deterministic snapshot ordered by opaque task handle.
func (store *Store) ListTasks(ctx context.Context) ([]domain.Task, error) {
	return listTasks(ctx, store.db)
}

// TaskSnapshot reads task rows and their advertised state version from one
// database snapshot so concurrent mutation cannot produce a mixed projection.
func (store *Store) TaskSnapshot(ctx context.Context) ([]domain.Task, int64, error) {
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("begin task snapshot: %w", err)
	}
	tasks, err := listTasks(ctx, transaction)
	if err != nil {
		return nil, 0, errors.Join(err, transaction.Rollback())
	}
	stateVersion, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return nil, 0, errors.Join(err, transaction.Rollback())
	}
	if err := transaction.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit task snapshot: %w", err)
	}
	return tasks, stateVersion, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func listTasks(ctx context.Context, source queryer) (tasks []domain.Task, resultErr error) {
	const query = `SELECT
        handle, schema_version, service_instance_id, managed_run_id,
        workspace_lease_id, state, shape, repository_id, base_revision,
        brief_revision, brief_revision_hash, acceptance_criteria_json,
        constraints_json, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    FROM tasks ORDER BY handle`
	rows, err := source.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, rows.Close())
	}()
	tasks = make([]domain.Task, 0)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	return tasks, nil
}

// GetTask returns one validated task record by its opaque handle.
func (store *Store) GetTask(ctx context.Context, handle string) (domain.Task, error) {
	return getTask(ctx, store.db, handle)
}

func getTask(ctx context.Context, source queryer, handle string) (domain.Task, error) {
	const query = `SELECT
        handle, schema_version, service_instance_id, managed_run_id,
        workspace_lease_id, state, shape, repository_id, base_revision,
        brief_revision, brief_revision_hash, acceptance_criteria_json,
        constraints_json, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    FROM tasks WHERE handle = ?`
	task, err := scanTask(source.QueryRowContext(ctx, query, handle))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Task{}, fmt.Errorf("get task: %w", application.ErrNotFound)
	}
	if err != nil {
		return domain.Task{}, fmt.Errorf("get task: %w", err)
	}
	return task, nil
}

// GetOperation returns one validated immutable operation outcome.
func (store *Store) GetOperation(ctx context.Context, id string) (domain.OperationRecord, error) {
	return getOperation(ctx, store.db, id)
}

func getOperation(ctx context.Context, source queryer, id string) (domain.OperationRecord, error) {
	const query = `SELECT
        id, schema_version, command, subject_digest, status, error_code,
        result_ref, state_version, created_at, updated_at
    FROM operations WHERE id = ?`
	var operation domain.OperationRecord
	var createdAt string
	var updatedAt string
	err := source.QueryRowContext(ctx, query, id).Scan(
		&operation.ID,
		&operation.SchemaVersion,
		&operation.Command,
		&operation.SubjectDigest,
		&operation.Status,
		&operation.ErrorCode,
		&operation.ResultRef,
		&operation.StateVersion,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.OperationRecord{}, fmt.Errorf("get operation: %w", application.ErrNotFound)
	}
	if err != nil {
		return domain.OperationRecord{}, fmt.Errorf("get operation: %w", err)
	}
	operation.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.OperationRecord{}, fmt.Errorf("get operation created time: %w", err)
	}
	operation.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.OperationRecord{}, fmt.Errorf("get operation updated time: %w", err)
	}
	if err := operation.Validate(); err != nil {
		return domain.OperationRecord{}, fmt.Errorf("validate stored operation: %w", err)
	}
	return operation, nil
}

// CurrentStateVersion returns the greatest durable record version, or zero for
// an empty store.
func (store *Store) CurrentStateVersion(ctx context.Context) (int64, error) {
	return currentStateVersion(ctx, store.db)
}

func currentStateVersion(ctx context.Context, source queryer) (int64, error) {
	const query = `SELECT COALESCE(MAX(state_version), 0) FROM (
        SELECT state_version FROM tasks
        UNION ALL
        SELECT state_version FROM operations
		UNION ALL
		SELECT state_version FROM reports
    )`
	var version int64
	if err := source.QueryRowContext(ctx, query).Scan(&version); err != nil {
		return 0, fmt.Errorf("read current state version: %w", err)
	}
	return version, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanTask(row rowScanner) (domain.Task, error) {
	var task domain.Task
	var acceptanceCriteria string
	var constraints string
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&task.Handle,
		&task.SchemaVersion,
		&task.ServiceInstanceID,
		&task.ManagedRunID,
		&task.WorkspaceLeaseID,
		&task.State,
		&task.Shape,
		&task.RepositoryID,
		&task.BaseRevision,
		&task.BriefRevision,
		&task.BriefRevisionHash,
		&acceptanceCriteria,
		&constraints,
		&task.ValidationProfile,
		&task.DeliveryMode,
		&task.WorkerProfileID,
		&task.ReportCursor,
		&task.StateVersion,
		&createdAt,
		&updatedAt,
	); err != nil {
		return domain.Task{}, err
	}
	if err := json.Unmarshal([]byte(acceptanceCriteria), &task.AcceptanceCriteria); err != nil {
		return domain.Task{}, fmt.Errorf("decode task acceptance criteria: %w", err)
	}
	if err := json.Unmarshal([]byte(constraints), &task.Constraints); err != nil {
		return domain.Task{}, fmt.Errorf("decode task constraints: %w", err)
	}
	var err error
	task.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse task created time: %w", err)
	}
	task.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return domain.Task{}, fmt.Errorf("parse task updated time: %w", err)
	}
	if err := task.Validate(); err != nil {
		return domain.Task{}, fmt.Errorf("validate stored task: %w", err)
	}
	return task, nil
}

func isConstraintError(err error) bool {
	type coder interface {
		Code() int
	}
	var databaseError coder
	return errors.As(err, &databaseError) && databaseError.Code()&0xff == sqliteConstraintCode
}

func formatTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}
