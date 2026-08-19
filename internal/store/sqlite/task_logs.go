package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// maximumTaskLogPage bounds one page so a follower cannot ask the service to
// materialize a task's whole history.
const maximumTaskLogPage = 500

// ReadTaskLogs reads one bounded page of one task's history from one source.
//
// Every source exposes the same monotonic cursor so following behaves
// identically whichever one an operator watches. The sources are queried
// separately rather than unioned: each carries different authority, and a
// combined stream would let a worker's claim read like a service fact.
func (store *Store) ReadTaskLogs(
	ctx context.Context,
	taskHandle string,
	source application.TaskLogSource,
	afterSequence int64,
	limit int,
) ([]application.TaskLogEntry, error) {
	if ctx == nil {
		return nil, errors.New("read task logs: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !source.Valid() {
		return nil, errors.New("read task logs: source is invalid")
	}
	if afterSequence < 0 {
		return nil, errors.New("read task logs: cursor is invalid")
	}
	if limit <= 0 || limit > maximumTaskLogPage {
		return nil, errors.New("read task logs: page size is invalid")
	}
	switch source {
	case application.LogSourceWorker:
		return store.readWorkerLogs(ctx, taskHandle, afterSequence, limit)
	case application.LogSourceService:
		return store.readServiceLogs(ctx, taskHandle, afterSequence, limit)
	default:
		return store.readValidationLogs(ctx, taskHandle, afterSequence, limit)
	}
}

// readWorkerLogs reads the reports a worker authored, ordered by the durable
// state version that accepted them.
func (store *Store) readWorkerLogs(
	ctx context.Context,
	taskHandle string,
	afterSequence int64,
	limit int,
) ([]application.TaskLogEntry, error) {
	rows, err := store.db.QueryContext(ctx, `
        SELECT state_version, accepted_at, kind, summary, external_key
        FROM reports
        WHERE task_handle = ? AND state_version > ?
        ORDER BY state_version
        LIMIT ?`, taskHandle, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("read worker logs: %w", err)
	}
	return scanTaskLogs(rows, application.LogSourceWorker, "read worker logs")
}

// readServiceLogs reads the task's own slice of the durable event log.
func (store *Store) readServiceLogs(
	ctx context.Context,
	taskHandle string,
	afterSequence int64,
	limit int,
) ([]application.TaskLogEntry, error) {
	rows, err := store.db.QueryContext(ctx, `
        SELECT sequence, occurred_at, kind, COALESCE(state, ''), COALESCE(reason, '')
        FROM service_events
        WHERE task_handle = ? AND sequence > ?
        ORDER BY sequence
        LIMIT ?`, taskHandle, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("read service logs: %w", err)
	}
	return scanTaskLogs(rows, application.LogSourceService, "read service logs")
}

// readValidationLogs reads which reviewed programs ran and how they ended.
//
// Only the operator-sanitized executable label travels, never a host path or an
// argument vector: what ran is operator detail, how it was invoked is launch
// authority.
func (store *Store) readValidationLogs(
	ctx context.Context,
	taskHandle string,
	afterSequence int64,
	limit int,
) ([]application.TaskLogEntry, error) {
	rows, err := store.db.QueryContext(ctx, `
        SELECT rowid, started_at, program_id, executable_label,
               state || COALESCE(' exit=' || exit_code, '')
        FROM validation_processes
        WHERE task_handle = ? AND rowid > ?
        ORDER BY rowid
        LIMIT ?`, taskHandle, afterSequence, limit)
	if err != nil {
		return nil, fmt.Errorf("read validation logs: %w", err)
	}
	return scanTaskLogs(rows, application.LogSourceValidation, "read validation logs")
}

// scanTaskLogs decodes the shared five-column shape every source projects into.
func scanTaskLogs(
	rows *sql.Rows,
	source application.TaskLogSource,
	subject string,
) ([]application.TaskLogEntry, error) {
	defer func() { _ = rows.Close() }()
	var entries []application.TaskLogEntry
	for rows.Next() {
		entry := application.TaskLogEntry{Source: source}
		var occurredAt string
		if err := rows.Scan(
			&entry.Sequence, &occurredAt, &entry.Label, &entry.Detail, &entry.Outcome,
		); err != nil {
			return nil, fmt.Errorf("decode task log entry: %w", err)
		}
		observed, parseErr := parseTime(occurredAt)
		if parseErr != nil {
			return nil, fmt.Errorf("decode task log time: %w", parseErr)
		}
		entry.OccurredAt = observed
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", subject, err)
	}
	return entries, nil
}
