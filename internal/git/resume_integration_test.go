package git_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// This uses the actual prepared worktree and lease-private Git layout. A flat
// workspace double cannot reproduce the shared index seeing the worker's
// committed files as dirty before the private branch is promoted.
func TestRegistry_ResumePromotesTheExactLeasePrivateWorkerCommit(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-private-resume")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-private-resume", "task-private-resume")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	private := createLeasePrivateCandidate(t, fixture, prepared)
	now := time.Date(2026, time.August, 23, 14, 0, 0, 0, time.UTC)
	task := domain.Task{
		SchemaVersion: 1, Handle: request.TaskHandle, ServiceInstanceID: "service-instance-private-resume",
		ManagedRunID: "managed-run-private-resume", WorkspaceLeaseID: "workspace-lease-private-resume",
		State: domain.TaskPaused, Shape: domain.ShapeShip, RepositoryID: request.RepositoryID,
		BaseRevision: request.BaseRevision, BriefRevision: 1,
		AcceptanceCriteria: []string{"The worker's committed changes remain available."},
		ValidationProfile:  "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "codex-reviewed", StateVersion: 8,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	task, err = task.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	store := &privateResumeStore{
		task: task,
		preparation: application.ManagedRunPreparation{
			ExternalRunRef: task.Handle, RequestedWorkspaceRoot: prepared.CanonicalPath,
			State: application.PreparationOpen,
		},
		preparationOperationID: request.OperationID,
	}
	interventions, err := application.NewInterventions(application.InterventionConfig{
		Store: store, Workspaces: registry, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := interventions.ResumeTask(context.Background(), application.ResumeTaskCommand{
		OperationID: "operation-resume-private-worker", TaskHandle: task.Handle,
	})
	if err != nil {
		t.Fatalf("ResumeTask(private worker commit) error = %v", err)
	}
	if result.Task.State != domain.TaskReady || store.resume.ObservedHeadRevision != private.head {
		t.Fatalf("resumed task = %#v, mutation = %#v", result.Task, store.resume)
	}
	if sharedHead := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"rev-parse", "HEAD"); sharedHead != private.head {
		t.Fatalf("shared head = %q, want private worker head %q", sharedHead, private.head)
	}
	if status := gitOutputAllowEmpty(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"status", "--porcelain=v2", "--untracked-files=all"); status != "" {
		t.Fatalf("resumed worktree status = %q, want clean", status)
	}
}

type privateResumeStore struct {
	task                   domain.Task
	preparation            application.ManagedRunPreparation
	preparationOperationID string
	resume                 application.TaskResumeMutation
}

func (*privateResumeStore) ReplayMutation(context.Context, string, string, string) (application.MutationResult, bool, error) {
	return application.MutationResult{}, false, nil
}

func (store *privateResumeStore) GetTask(context.Context, string) (domain.Task, error) {
	return store.task, nil
}

func (store *privateResumeStore) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return store.preparation, nil
}

func (store *privateResumeStore) ReadCandidateHandoffAuthority(context.Context, string) (application.CandidateHandoffAuthority, error) {
	return application.CandidateHandoffAuthority{
		Task: store.task, Preparation: store.preparation,
		PreparationOperationID: store.preparationOperationID,
	}, nil
}

func (store *privateResumeStore) CommitTaskResume(_ context.Context, mutation application.TaskResumeMutation) (application.MutationResult, error) {
	store.resume = mutation
	resumed := store.task
	resumed.State = domain.TaskReady
	return application.MutationResult{Task: resumed}, nil
}

func (*privateResumeStore) CommitTaskHandback(context.Context, application.TaskHandbackMutation) (application.MutationResult, error) {
	return application.MutationResult{}, errors.New("private resume fixture does not hand back")
}

func (*privateResumeStore) CommitTaskReplace(context.Context, application.TaskReplaceMutation) (application.MutationResult, error) {
	return application.MutationResult{}, errors.New("private resume fixture does not replace")
}
