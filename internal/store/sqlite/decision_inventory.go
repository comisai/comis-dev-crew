package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// ListTaskDecisions inventories every open decision, optionally scoped to one
// task. An empty task handle reads the whole fleet.
//
// Open uses the same predicate cleanup and re-surfacing use — a decision report
// with no resolution carrying its key — so the three surfaces can never disagree
// about which questions are still live. The outbox is joined on the left so a
// decision whose delivery row is missing reads as not yet asked rather than
// vanishing from the inventory: an unexplained absence is the one answer this
// read must never give.
func (store *Store) ListTaskDecisions(
	ctx context.Context,
	taskHandle string,
) ([]application.TaskDecision, error) {
	if ctx == nil {
		return nil, errors.New("list task decisions: context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := store.db.QueryContext(ctx, `
        SELECT d.task_handle, d.external_key, d.summary, d.details, d.accepted_at,
               o.delivered_at, COALESCE(s.surface_count, 0), COALESCE(s.last_surfaced_at, '')
        FROM reports d
        LEFT JOIN comis_report_outbox o
            ON o.task_handle = d.task_handle AND o.local_report_id = d.local_report_id
        LEFT JOIN task_decision_surfacings s
            ON s.task_handle = d.task_handle AND s.external_key = d.external_key
        WHERE d.kind = 'decision' AND (? = '' OR d.task_handle = ?) AND `+
		decisionStillOpenClause("d")+`
        ORDER BY d.task_handle, d.external_key`, taskHandle, taskHandle)
	if err != nil {
		return nil, fmt.Errorf("list task decisions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var decisions []application.TaskDecision
	for rows.Next() {
		decision, err := scanTaskDecision(rows)
		if err != nil {
			return nil, err
		}
		decisions = append(decisions, decision)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list task decisions: %w", err)
	}
	return decisions, nil
}

func scanTaskDecision(rows *sql.Rows) (application.TaskDecision, error) {
	var decision application.TaskDecision
	var reportedAt, lastSurfaced string
	var deliveredAt sql.NullString
	if err := rows.Scan(
		&decision.TaskHandle, &decision.ExternalKey, &decision.Question, &decision.Detail,
		&reportedAt, &deliveredAt, &decision.Airings, &lastSurfaced,
	); err != nil {
		return application.TaskDecision{}, fmt.Errorf("decode task decision: %w", err)
	}
	reported, err := parseTime(reportedAt)
	if err != nil {
		return application.TaskDecision{}, fmt.Errorf("decode task decision time: %w", err)
	}
	decision.ReportedAt = reported
	decision.Status = application.DecisionAwaitingHost
	if deliveredAt.Valid {
		asked, parseErr := parseTime(deliveredAt.String)
		if parseErr != nil {
			return application.TaskDecision{}, fmt.Errorf("decode task decision delivery time: %w", parseErr)
		}
		decision.Status = application.DecisionAwaitingHuman
		decision.AskedAt = &asked
		// The delivery is the first airing; the ledger counts the repeats on
		// top of it, matching what the cadence measures from.
		decision.Airings++
		decision.LastAiringAt = &asked
	}
	if lastSurfaced != "" {
		repeated, parseErr := parseTime(lastSurfaced)
		if parseErr != nil {
			return application.TaskDecision{}, fmt.Errorf("decode task decision surfacing time: %w", parseErr)
		}
		if decision.LastAiringAt == nil || repeated.After(*decision.LastAiringAt) {
			decision.LastAiringAt = &repeated
		}
	}
	return decision, nil
}
