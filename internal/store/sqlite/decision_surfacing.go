package sqlite

import (
	"context"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// The ledger is keyed by the decision, not by the wake, because the cadence is
// a property of the question rather than of how often something looked. It is
// durable so a restart replays at worst one repeat: an in-memory count would
// make every open decision due again on every boot, and a service that restarts
// often would wake the liaison with the whole backlog each time.
const decisionSurfacingMigration = `
CREATE TABLE task_decision_surfacings (
    task_handle TEXT NOT NULL,
    external_key TEXT NOT NULL,
    first_surfaced_at TEXT NOT NULL,
    last_surfaced_at TEXT NOT NULL,
    surface_count INTEGER NOT NULL,
    PRIMARY KEY(task_handle, external_key),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (29, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// OpenDecisionsAwaitingHuman lists every decision with no matching resolution,
// together with how often it has already been raised.
//
// Open is decided by the same predicate cleanup uses: a decision report with no
// resolution carrying its key. Deriving it from one place is what stops cleanup
// and re-surfacing from disagreeing about which questions are still live.
func (store *Store) OpenDecisionsAwaitingHuman(ctx context.Context) ([]application.OpenDecision, error) {
	if ctx == nil {
		return nil, errors.New("read open decisions: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
        SELECT d.task_handle, d.external_key,
               COALESCE(s.surface_count, 0), COALESCE(s.last_surfaced_at, '')
        FROM reports d
        LEFT JOIN task_decision_surfacings s
            ON s.task_handle = d.task_handle AND s.external_key = d.external_key
        WHERE d.kind = 'decision' AND NOT EXISTS (
            SELECT 1 FROM reports r
            WHERE r.task_handle = d.task_handle
              AND r.kind = 'resolution' AND r.external_key = d.external_key
        )
        ORDER BY d.task_handle, d.external_key`)
	if err != nil {
		return nil, fmt.Errorf("read open decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var open []application.OpenDecision
	for rows.Next() {
		var decision application.OpenDecision
		var lastSurfaced string
		if err := rows.Scan(&decision.TaskHandle, &decision.ExternalKey, &decision.SurfaceCount, &lastSurfaced); err != nil {
			return nil, fmt.Errorf("decode open decision: %w", err)
		}
		if lastSurfaced != "" {
			observed, parseErr := parseTime(lastSurfaced)
			if parseErr != nil {
				return nil, fmt.Errorf("decode decision surfacing time: %w", parseErr)
			}
			decision.LastSurfacedAt = observed
		}
		open = append(open, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read open decisions: %w", err)
	}
	return open, nil
}

// RecordDecisionSurfaced notes that one decision was put in front of the
// liaison again, keeping the first sighting so the age of an unanswered
// question stays legible.
func (store *Store) RecordDecisionSurfaced(
	ctx context.Context,
	mutation application.DecisionSurfacedMutation,
) error {
	if ctx == nil {
		return errors.New("record surfaced decision: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := store.db.ExecContext(ctx, `
        INSERT INTO task_decision_surfacings(
            task_handle, external_key, first_surfaced_at, last_surfaced_at, surface_count
        ) VALUES (?, ?, ?, ?, 1)
        ON CONFLICT(task_handle, external_key) DO UPDATE SET
            last_surfaced_at = excluded.last_surfaced_at,
            surface_count = task_decision_surfacings.surface_count + 1`,
		mutation.TaskHandle, mutation.ExternalKey,
		formatTime(mutation.At), formatTime(mutation.At),
	); err != nil {
		return fmt.Errorf("record surfaced decision: %w", err)
	}
	return nil
}
