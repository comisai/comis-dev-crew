package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// taskMutationBody performs the part of a durable mutation that differs between
// commands: it resolves the subject, checks the command's own preconditions, and
// returns the task state to persist. Everything around it — the transaction, the
// replay check, the state version, the operation record, the commit — is the
// same for every command and lives in commitTaskMutation.
type taskMutationBody func(context.Context, *sql.Tx) (domain.Task, error)

// taskMutationSpec is one command's identity for the shared skeleton.
type taskMutationSpec struct {
	Command       string
	OperationID   string
	SubjectDigest string
	At            time.Time
	Label         string
}

// commitTaskMutation runs the transaction skeleton every task-state command
// shares.
//
// Each command used to carry its own copy: begin, rollback, replay, version,
// update, operation insert, commit — seven identical failure returns per
// command, none of which a test can reach without breaking the database
// underneath it. Duplicating them made every new command dilute the package's
// coverage with statements that assert nothing new, so the skeleton is written
// once and the commands supply only what actually differs.
func commitTaskMutation(
	ctx context.Context,
	store *Store,
	spec taskMutationSpec,
	body taskMutationBody,
) (application.MutationResult, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.MutationResult{}, fmt.Errorf("begin %s: %w", spec.Label, err)
	}
	defer func() { _ = transaction.Rollback() }()
	if replay, found, err := mutationReplay(
		ctx, transaction, spec.OperationID, spec.Command, spec.SubjectDigest,
	); err != nil {
		return application.MutationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return replayResult(ctx, transaction, replay)
	}
	updated, err := body(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.MutationResult{}, err
	}
	updated.StateVersion = stateVersion
	if err := updateTaskState(ctx, transaction, updated); err != nil {
		return application.MutationResult{}, err
	}
	operation := completedMutationOperation(
		spec.OperationID, spec.Command, spec.SubjectDigest, updated.Handle, stateVersion, spec.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.MutationResult{}, fmt.Errorf("insert %s operation: %w", spec.Label, application.ErrConflict)
		}
		return application.MutationResult{}, fmt.Errorf("insert %s operation: %w", spec.Label, err)
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit %s: %w", spec.Label, err)
	}
	return application.MutationResult{Task: updated, Operation: operation}, nil
}
