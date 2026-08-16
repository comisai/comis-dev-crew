package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func insertReplayConflict(
	ctx context.Context,
	target execer,
	original domain.OperationRecord,
	presentedCommand, presentedDigest string,
) error {
	const statement = `INSERT INTO operation_replay_conflicts (
		operation_id, original_command, original_subject_digest,
		presented_command, presented_subject_digest
	) VALUES (?, ?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement, original.ID, original.Command,
		original.SubjectDigest, presentedCommand, presentedDigest)
	return err
}

func commitReplayConflict(transaction *sql.Tx, cause error) error {
	if !errors.Is(cause, application.ErrConflict) {
		return cause
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit replay-conflict audit: %w", err)
	}
	return cause
}

func nextMutationStateVersion(ctx context.Context, transaction *sql.Tx) (int64, error) {
	current, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return 0, err
	}
	if current == math.MaxInt64 {
		return 0, errors.New("state version is exhausted")
	}
	return current + 1, nil
}

func completedMutationOperation(id, command, digest, resultRef string, stateVersion int64, at time.Time) domain.OperationRecord {
	return domain.OperationRecord{
		SchemaVersion: 1, ID: id, Command: command, SubjectDigest: digest,
		Status: domain.OperationCompleted, ResultRef: resultRef, StateVersion: stateVersion,
		CreatedAt: at, UpdatedAt: at,
	}
}
