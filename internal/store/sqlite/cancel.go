package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CommitManagedRunCancel stops one activated run at the host's request.
//
// A run this service has already settled answers with that settled task instead
// of refusing: the host recorded its decision before sending, so treating an
// already-terminal run as a precondition failure would make a safe repeat look
// like a fault. Artifacts are preserved — cancellation stops work, it does not
// discard it, and removal has its own evidence-gated path.
func (store *Store) CommitManagedRunCancel(
	ctx context.Context,
	mutation application.ManagedRunCancelMutation,
) (application.MutationResult, error) {
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandCancelManagedRun,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "managed-run cancel",
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTaskByManagedRun(ctx, transaction, mutation.ManagedRunID)
		if err != nil {
			return domain.Task{}, err
		}
		if task.ServiceInstanceID != mutation.ServiceInstanceID || mutation.At.Location() != time.UTC {
			return domain.Task{}, fmt.Errorf("managed-run cancel join: %w", application.ErrPrecondition)
		}
		// Two operators can both decide to stop the same run. The second one
		// reports the settled task rather than transitioning it again.
		if task.State == domain.TaskCancelled {
			return task, nil
		}
		updated, err := task.ApplyTransition(domain.TransitionCancelRequested, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("apply managed-run cancel: %w", err)
		}
		return updated, nil
	})
}
