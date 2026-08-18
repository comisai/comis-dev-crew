package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CommitTaskVerify moves one task into validation at an operator's request.
//
// It only opens validation; it does not judge. The reviewed profile, the
// candidate inspection, the unresolved-decision check and the evidence refresh
// all stay with the supervisor that already owns them, and this command's whole
// effect is to say "check it now" instead of waiting for the worker to declare a
// candidate. Deciding the outcome here would be a second judge with a different
// view of the same tree.
//
// Nothing guards against a worker still writing. An unverifiable tree is already
// judged unknown by the candidate inspection, which is the honest answer; a
// cleanliness guard here would be a parallel rule at a convenient layer that
// hides the case rather than reporting it.
func (store *Store) CommitTaskVerify(
	ctx context.Context,
	mutation application.TaskVerifyMutation,
) (application.MutationResult, error) {
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandVerifyTask,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "task verify",
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		if mutation.At.Before(task.UpdatedAt) {
			return domain.Task{}, fmt.Errorf("task verify time: %w", application.ErrPrecondition)
		}
		// A task already validating is left exactly as it is. Restarting it would
		// abandon a validation run that is mid-flight and whose process the
		// service is still tracking.
		if task.State == domain.TaskValidating {
			return task, nil
		}
		updated, err := task.ApplyTransition(domain.TransitionValidationStarted, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("apply task verify: %w", err)
		}
		return updated, nil
	})
}
