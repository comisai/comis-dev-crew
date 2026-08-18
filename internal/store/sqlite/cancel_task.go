package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CommitTaskCancel stops one task at an operator's request.
//
// It is the operator sibling of the host-initiated managed-run cancel: same
// transition, same artifact-preserving guarantee, reached by task handle instead
// of by run. Both exist because the two authorities are genuinely different —
// the host cancels a run it owns, an operator cancels work they asked for — and
// collapsing them would make an operator's cancel require a run reference they
// have no reason to hold.
//
// Artifacts are preserved. Cancellation stops work; removing it is cleanup, and
// cleanup is evidence-gated precisely so that stopping and discarding cannot be
// confused for one another.
func (store *Store) CommitTaskCancel(
	ctx context.Context,
	mutation application.TaskCancelMutation,
) (application.MutationResult, error) {
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandCancelTask,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "task cancel",
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		if mutation.At.Location() != time.UTC {
			return domain.Task{}, fmt.Errorf("task cancel time: %w", application.ErrPrecondition)
		}
		// Two operators can decide to stop the same work. The second reports the
		// settled task rather than transitioning it again, so a safe repeat does
		// not read as a fault.
		if task.State == domain.TaskCancelled {
			return task, nil
		}
		updated, err := task.ApplyTransition(domain.TransitionCancelRequested, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("apply task cancel: %w", err)
		}
		// A cancelled task's worker is gone, so a pause request standing against
		// it can never be answered. Leaving it would show a pause as forever
		// pending on work that already stopped.
		if err := clearPauseRequest(ctx, transaction, task.Handle); err != nil {
			return domain.Task{}, err
		}
		return updated, nil
	})
}
