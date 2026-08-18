package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Promotion records which scout a ship task came from. It is a link, not a
// state: the scout keeps its own identity, shape, evidence and history, and a
// promotion that rewrote the scout would destroy the investigation the ship task
// is meant to be justified by.
const scoutPromotionMigration = `
CREATE TABLE scout_promotions (
    operation_id TEXT PRIMARY KEY,
    scout_task_handle TEXT NOT NULL,
    ship_task_handle TEXT NOT NULL UNIQUE,
    scout_evidence_digest TEXT NOT NULL,
    promoted_at TEXT NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(scout_task_handle) REFERENCES tasks(handle),
    FOREIGN KEY(ship_task_handle) REFERENCES tasks(handle)
);
CREATE INDEX scout_promotions_scout_idx ON scout_promotions(scout_task_handle);
INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES (24, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

// ReadScoutPromotionSource proves one scout is promotable and returns the
// contract fields a ship revision inherits from it.
//
// A scout with no sealed evidence has produced no investigation, so there is
// nothing for a ship task to be justified by and nothing to preserve. Refusing
// here rather than promoting an empty scout keeps the link meaningful: every
// promotion points at a report someone can read.
func (store *Store) ReadScoutPromotionSource(
	ctx context.Context,
	scoutTaskHandle string,
) (application.ScoutPromotionSource, error) {
	if err := store.ready(ctx); err != nil {
		return application.ScoutPromotionSource{}, err
	}
	task, err := getTask(ctx, store.db, scoutTaskHandle)
	if err != nil {
		return application.ScoutPromotionSource{}, err
	}
	if task.Shape != domain.ShapeScout {
		return application.ScoutPromotionSource{}, fmt.Errorf("read scout promotion shape: %w", application.ErrPrecondition)
	}
	sealed, _, err := store.LatestCandidateEvidence(ctx, scoutTaskHandle)
	if err != nil {
		return application.ScoutPromotionSource{}, err
	}
	return application.ScoutPromotionSource{
		ScoutTaskHandle: task.Handle,
		RepositoryID:    task.RepositoryID,
		BaseRevision:    task.BaseRevision,
		EvidenceDigest:  sealed.Digest(),
	}, nil
}

// CommitScoutPromotionLink records that one ship task was created from one
// scout, in the same transaction that proves both tasks exist.
func (store *Store) CommitScoutPromotionLink(
	ctx context.Context,
	link application.ScoutPromotionLink,
) error {
	if err := store.ready(ctx); err != nil {
		return err
	}
	if link.PromotedAt.Location() != time.UTC {
		return fmt.Errorf("record scout promotion time: %w", application.ErrPrecondition)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin scout promotion link: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if existing, found, err := findScoutPromotion(ctx, transaction, link.OperationID); err != nil {
		return err
	} else if found {
		// A repeat of the same promotion is the safe case. A repeat that names a
		// different scout or ship task is a different promotion wearing the same
		// operation identity, and committing it would attribute one
		// investigation's evidence to work it never covered.
		if existing.ScoutTaskHandle != link.ScoutTaskHandle ||
			existing.ShipTaskHandle != link.ShipTaskHandle ||
			existing.EvidenceDigest != link.EvidenceDigest {
			return fmt.Errorf("scout promotion altered replay: %w", application.ErrConflict)
		}
		return nil
	}
	if _, err := transaction.ExecContext(ctx,
		`INSERT INTO scout_promotions(
            operation_id, scout_task_handle, ship_task_handle, scout_evidence_digest, promoted_at
        ) VALUES (?, ?, ?, ?, ?)`,
		link.OperationID, link.ScoutTaskHandle, link.ShipTaskHandle,
		link.EvidenceDigest, formatTime(link.PromotedAt),
	); err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("record scout promotion: %w", application.ErrConflict)
		}
		return fmt.Errorf("record scout promotion: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit scout promotion link: %w", err)
	}
	return nil
}

// ScoutPromotion reports the promotion one ship task came from, when it came
// from one at all.
func (store *Store) ScoutPromotion(
	ctx context.Context,
	shipTaskHandle string,
) (application.ScoutPromotionLink, bool, error) {
	if err := store.ready(ctx); err != nil {
		return application.ScoutPromotionLink{}, false, err
	}
	const query = `SELECT operation_id, scout_task_handle, ship_task_handle,
        scout_evidence_digest, promoted_at
    FROM scout_promotions WHERE ship_task_handle = ?`
	return scanScoutPromotion(store.db.QueryRowContext(ctx, query, shipTaskHandle))
}

func findScoutPromotion(
	ctx context.Context,
	source queryer,
	operationID string,
) (application.ScoutPromotionLink, bool, error) {
	const query = `SELECT operation_id, scout_task_handle, ship_task_handle,
        scout_evidence_digest, promoted_at
    FROM scout_promotions WHERE operation_id = ?`
	return scanScoutPromotion(source.QueryRowContext(ctx, query, operationID))
}

func scanScoutPromotion(row rowScanner) (application.ScoutPromotionLink, bool, error) {
	var link application.ScoutPromotionLink
	var promotedAt string
	err := row.Scan(&link.OperationID, &link.ScoutTaskHandle, &link.ShipTaskHandle,
		&link.EvidenceDigest, &promotedAt)
	if err == sql.ErrNoRows {
		return application.ScoutPromotionLink{}, false, nil
	}
	if err != nil {
		return application.ScoutPromotionLink{}, false, fmt.Errorf("read scout promotion link: %w", err)
	}
	parsed, err := parseTime(promotedAt)
	if err != nil {
		return application.ScoutPromotionLink{}, false, fmt.Errorf("read scout promotion time: %w", err)
	}
	link.PromotedAt = parsed
	return link, true, nil
}

// ready refuses a caller that has lost its store or its context before any
// database work begins. Each entry point carried its own copy of this check,
// which meant three identical branches asserting one rule.
func (store *Store) ready(ctx context.Context) error {
	if store == nil || store.db == nil || ctx == nil {
		return fmt.Errorf("store is unavailable: %w", application.ErrPrecondition)
	}
	return ctx.Err()
}
