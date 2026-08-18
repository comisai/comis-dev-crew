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
	// Record runs after the operation is inserted and before the commit, for
	// commands that write their own durable trail alongside the task. It sees the
	// persisted task, so a row it writes cannot disagree with the state version
	// the same transaction assigned, and the operation its trail references
	// already exists.
	Record func(context.Context, *sql.Tx, domain.Task) error
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
	// Every durable timestamp is UTC. A local-zone time would be stored as a wall
	// clock reading whose meaning depends on where the service happened to run,
	// and two records written either side of a DST change would compare wrongly.
	// The check lives here rather than in each command because it is the same
	// rule everywhere, and a command that forgot it would look correct until a
	// deployment moved.
	if spec.At.Location() != time.UTC {
		return application.MutationResult{}, fmt.Errorf("%s time: %w", spec.Label, application.ErrPrecondition)
	}
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
	// Record runs after the operation exists, because a command's own trail
	// references it. Running it earlier leaves that reference dangling inside the
	// transaction and the insert is refused.
	if spec.Record != nil {
		if err := spec.Record(ctx, transaction, updated); err != nil {
			return application.MutationResult{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return application.MutationResult{}, fmt.Errorf("commit %s: %w", spec.Label, err)
	}
	return application.MutationResult{Task: updated, Operation: operation}, nil
}
