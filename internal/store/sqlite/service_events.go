package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The log is append-only and its sequence is the follower's cursor. Events are
// written in the same transaction as the change they describe, so the log can
// never claim a transition the durable state did not take, and a crash can never
// commit a state whose event was lost.
//
// Every column is an identity, a closed discriminator, a version or a time. No
// column can hold a question, an objective, a path or a branch, so the stream is
// content-free by construction rather than by the care of each writer.
const serviceEventMigration = `
CREATE TABLE service_events (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    occurred_at TEXT NOT NULL,
    kind TEXT NOT NULL,
    task_handle TEXT,
    state TEXT,
    reason TEXT,
    state_version INTEGER
);
CREATE INDEX service_events_task_idx ON service_events(task_handle, sequence);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (30, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// maximumServiceEventPage bounds one read so a follower cannot ask the service
// to materialize an unbounded page.
const maximumServiceEventPage = 500

// appendServiceEvent records one content-free event inside the caller's
// transaction. It is unexported so the log can only grow beside a real change.
func appendServiceEvent(
	ctx context.Context,
	transaction *sql.Tx,
	event application.ServiceEvent,
) error {
	if !event.Kind.Valid() {
		return errors.New("append service event: kind is invalid")
	}
	if event.OccurredAt.IsZero() {
		return errors.New("append service event: observation time is required")
	}
	const insert = `INSERT INTO service_events(
        occurred_at, kind, task_handle, state, reason, state_version
    ) VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		formatTime(event.OccurredAt), string(event.Kind), event.TaskHandle,
		string(event.State), event.Reason, event.StateVersion,
	); err != nil {
		return fmt.Errorf("append service event: %w", err)
	}
	return nil
}

// appendTaskStateEvent records one durable task transition.
func appendTaskStateEvent(ctx context.Context, transaction *sql.Tx, task domain.Task) error {
	return appendServiceEvent(ctx, transaction, application.ServiceEvent{
		OccurredAt: task.UpdatedAt, Kind: application.EventTaskStateChanged,
		TaskHandle: task.Handle, State: task.State, StateVersion: task.StateVersion,
	})
}

// appendDecisionEvent records a keyed question opening or closing.
//
// Only the key travels, never the question: the key is an opaque operator-facing
// identifier, while the question itself is private task detail.
func appendDecisionEvent(
	ctx context.Context,
	transaction *sql.Tx,
	accepted domain.AcceptedReport,
) error {
	var kind application.ServiceEventKind
	switch accepted.Report.Kind {
	case domain.ReportDecision:
		kind = application.EventDecisionOpened
	case domain.ReportResolution:
		kind = application.EventDecisionResolved
	default:
		return nil
	}
	return appendServiceEvent(ctx, transaction, application.ServiceEvent{
		OccurredAt: accepted.AcceptedAt, Kind: kind, TaskHandle: accepted.TaskHandle,
		Reason: accepted.Report.ExternalKey, StateVersion: accepted.StateVersion,
	})
}

// ReadServiceEvents returns the bounded page of events after the given cursor.
//
// A cursor of zero starts at the beginning. The page is bounded so a follower
// cannot ask the service to materialize the whole log, and an exhausted cursor
// returns an empty page rather than blocking: waiting belongs to the caller,
// which can resume from the last sequence it saw.
func (store *Store) ReadServiceEvents(
	ctx context.Context,
	afterSequence int64,
	limit int,
	taskHandle string,
) ([]application.ServiceEvent, error) {
	if ctx == nil {
		return nil, errors.New("read service events: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if afterSequence < 0 {
		return nil, errors.New("read service events: cursor is invalid")
	}
	if limit <= 0 || limit > maximumServiceEventPage {
		return nil, errors.New("read service events: page size is invalid")
	}
	// The scope is applied in the query rather than after the page is built, so
	// a task's events cannot be pushed off the page by a busy fleet before the
	// caller ever sees them.
	rows, err := store.db.QueryContext(ctx, `
        SELECT sequence, occurred_at, kind, COALESCE(task_handle, ''), COALESCE(state, ''),
               COALESCE(reason, ''), COALESCE(state_version, 0)
        FROM service_events
        WHERE sequence > ? AND (? = '' OR task_handle = ?)
        ORDER BY sequence
        LIMIT ?`, afterSequence, taskHandle, taskHandle, limit)
	if err != nil {
		return nil, fmt.Errorf("read service events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]application.ServiceEvent, 0, limit)
	for rows.Next() {
		var event application.ServiceEvent
		var occurredAt string
		if err := rows.Scan(
			&event.Sequence, &occurredAt, &event.Kind, &event.TaskHandle,
			&event.State, &event.Reason, &event.StateVersion,
		); err != nil {
			return nil, fmt.Errorf("decode service event: %w", err)
		}
		observed, parseErr := parseTime(occurredAt)
		if parseErr != nil {
			return nil, fmt.Errorf("decode service event time: %w", parseErr)
		}
		event.OccurredAt = observed
		// An unreadable discriminator is a failure rather than a row a follower
		// would have to guess at.
		if !event.Kind.Valid() {
			return nil, errors.New("read service events: event kind is invalid")
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read service events: %w", err)
	}
	return events, nil
}
