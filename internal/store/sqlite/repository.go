package sqlite

import (
	"context"
	"database/sql"
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
	if err := task.Validate(); err != nil {
		return fmt.Errorf("create task: %w", err)
	}
	const statement = `INSERT INTO tasks (
        handle, schema_version, state, shape, repository_id, base_revision,
        brief_revision, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := store.db.ExecContext(ctx, statement,
		task.Handle,
		task.SchemaVersion,
		task.State,
		task.Shape,
		task.RepositoryID,
		task.BaseRevision,
		task.BriefRevision,
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
			return fmt.Errorf("create task: %w", application.ErrConflict)
		}
		return fmt.Errorf("create task: %w", err)
	}
	return nil
}

// RecordOperation inserts one immutable operation outcome for later replay and
// uncertain-result reconciliation.
func (store *Store) RecordOperation(ctx context.Context, operation domain.OperationRecord) error {
	if err := operation.Validate(); err != nil {
		return fmt.Errorf("record operation: %w", err)
	}
	const statement = `INSERT INTO operations (
        id, schema_version, command, subject_digest, status, error_code,
        state_version, created_at, updated_at
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := store.db.ExecContext(ctx, statement,
		operation.ID,
		operation.SchemaVersion,
		operation.Command,
		operation.SubjectDigest,
		operation.Status,
		operation.ErrorCode,
		operation.StateVersion,
		formatTime(operation.CreatedAt),
		formatTime(operation.UpdatedAt),
	)
	if err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("record operation: %w", application.ErrConflict)
		}
		return fmt.Errorf("record operation: %w", err)
	}
	return nil
}

// ListTasks returns a deterministic snapshot ordered by opaque task handle.
func (store *Store) ListTasks(ctx context.Context) ([]domain.Task, error) {
	const query = `SELECT
        handle, schema_version, state, shape, repository_id, base_revision,
        brief_revision, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    FROM tasks ORDER BY handle`
	rows, err := store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()
	tasks := make([]domain.Task, 0)
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
	const query = `SELECT
        handle, schema_version, state, shape, repository_id, base_revision,
        brief_revision, validation_profile, delivery_mode, worker_profile_id,
        report_cursor, state_version, created_at, updated_at
    FROM tasks WHERE handle = ?`
	task, err := scanTask(store.db.QueryRowContext(ctx, query, handle))
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
	const query = `SELECT
        id, schema_version, command, subject_digest, status, error_code,
        state_version, created_at, updated_at
    FROM operations WHERE id = ?`
	var operation domain.OperationRecord
	var createdAt string
	var updatedAt string
	err := store.db.QueryRowContext(ctx, query, id).Scan(
		&operation.ID,
		&operation.SchemaVersion,
		&operation.Command,
		&operation.SubjectDigest,
		&operation.Status,
		&operation.ErrorCode,
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
	const query = `SELECT COALESCE(MAX(state_version), 0) FROM (
        SELECT state_version FROM tasks
        UNION ALL
        SELECT state_version FROM operations
    )`
	var version int64
	if err := store.db.QueryRowContext(ctx, query).Scan(&version); err != nil {
		return 0, fmt.Errorf("read current state version: %w", err)
	}
	return version, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanTask(row rowScanner) (domain.Task, error) {
	var task domain.Task
	var createdAt string
	var updatedAt string
	if err := row.Scan(
		&task.Handle,
		&task.SchemaVersion,
		&task.State,
		&task.Shape,
		&task.RepositoryID,
		&task.BaseRevision,
		&task.BriefRevision,
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
