package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CommitTaskResume returns one paused task to its existing worker.
//
// The caller has already proven the worktree is exactly as the worker left it.
// That proof is the whole precondition: resuming the same worker onto a tree
// someone edited would continue from a brief and an evidence set that describe a
// different tree, and the worker would have no way to notice. When the tree did
// change, the operator's path is handback, which revalidates.
func (store *Store) CommitTaskResume(
	ctx context.Context,
	mutation application.TaskResumeMutation,
) (application.MutationResult, error) {
	return commitTaskMutation(ctx, store, taskMutationSpec{
		Command:     commandResumeTask,
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		At: mutation.At, Label: "task resume",
	}, func(ctx context.Context, transaction *sql.Tx) (domain.Task, error) {
		task, err := getTask(ctx, transaction, mutation.TaskHandle)
		if err != nil {
			return domain.Task{}, err
		}
		if mutation.At.Location() != time.UTC || mutation.At.Before(task.UpdatedAt) {
			return domain.Task{}, fmt.Errorf("task resume time: %w", application.ErrPrecondition)
		}
		if task.State != domain.TaskPaused {
			return domain.Task{}, fmt.Errorf("task resume state: %w", application.ErrPrecondition)
		}
		// The head observed at resume is recorded against the task the caller
		// inspected. A resume committed against a task whose brief moved on since
		// the inspection would be resuming onto state the caller never saw.
		if mutation.ObservedHeadRevision == "" ||
			domain.ValidateGitRevision(mutation.ObservedHeadRevision) != nil {
			return domain.Task{}, fmt.Errorf("task resume head: %w", application.ErrPrecondition)
		}
		updated, err := task.ApplyTransition(domain.TransitionResumed, mutation.At)
		if err != nil {
			return domain.Task{}, fmt.Errorf("apply task resume: %w", err)
		}
		// No pause request is cleared here. The only route into the paused state
		// is a worker's own paused report, which clears the request it answers in
		// the same transaction, so a paused task never carries one. Clearing
		// again would be an unreachable branch asserting an invariant that
		// belongs to the report path.
		return updated, nil
	})
}
