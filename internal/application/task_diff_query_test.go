package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type taskDiffStub struct {
	view     TaskDiffView
	err      error
	requests []TaskDiffRequest
}

func (stub *taskDiffStub) InspectTaskDiff(
	_ context.Context,
	request TaskDiffRequest,
) (TaskDiffView, error) {
	stub.requests = append(stub.requests, request)
	return stub.view, stub.err
}

func diffTaskFixture() domain.Task {
	task := domain.Task{
		SchemaVersion: 1, Handle: "task-0001", ServiceInstanceID: "service-instance-0001",
		State: domain.TaskWorking, Shape: domain.ShapeShip, RepositoryID: "product-api",
		BaseRevision: strings.Repeat("a", 40), BriefRevision: 1,
		AcceptanceCriteria: []string{"The requested change is proven."},
		Constraints:        []string{"Preserve unrelated changes."},
		ValidationProfile:  "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "codex-standard", StateVersion: 3,
		ManagedRunID: "managed-run-0001", WorkspaceLeaseID: "workspace-lease-0001",
		CreatedAt: time.Unix(1_800_000_000, 0).UTC(),
		UpdatedAt: time.Unix(1_800_000_000, 0).UTC(),
	}
	pinned, err := task.PinBriefRevision()
	if err != nil {
		panic(err)
	}
	return pinned
}

func diffQueryFixture(t *testing.T, stub *taskDiffStub) (*Queries, *queryRepository) {
	t.Helper()
	repository := &queryRepository{
		tasks: []domain.Task{diffTaskFixture()}, stateVersion: 3,
		preparation: ManagedRunPreparation{RequestedWorkspaceRoot: "/approved/worktrees/task-0001"},
	}
	queries, err := NewQueries(QueryConfig{
		Repository: repository, TaskDiffs: stub,
		Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	return queries, repository
}

// The diff is read against the task's own pinned base and its own recorded
// workspace root. Taking either from the caller would let a request describe
// work in a tree the task does not own.
func TestDiffTask_ReadsTheTaskOwnBaseAndWorkspaceRoot(t *testing.T) {
	stub := &taskDiffStub{view: TaskDiffView{
		BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40),
		Committed: []TaskFileChange{{Path: "internal/thing.go", Added: 12, Deleted: 3}},
	}}
	queries, _ := diffQueryFixture(t, stub)

	view, err := queries.DiffTask(context.Background(), "task-0001")
	if err != nil {
		t.Fatalf("DiffTask() error = %v", err)
	}
	if view.SchemaVersion != 1 || view.TaskHandle != "task-0001" || view.StateVersion != 3 {
		t.Fatalf("diff view = %#v", view)
	}
	if len(stub.requests) != 1 {
		t.Fatalf("inspector requests = %#v", stub.requests)
	}
	request := stub.requests[0]
	if request.TaskHandle != "task-0001" || request.RepositoryID != "product-api" ||
		request.BaseRevision != strings.Repeat("a", 40) ||
		request.WorktreePath != "/approved/worktrees/task-0001" {
		t.Fatalf("inspector request = %#v", request)
	}
	if len(view.Committed) != 1 || view.Committed[0].Path != "internal/thing.go" {
		t.Fatalf("committed changes = %#v", view.Committed)
	}
}

// A truncated listing is stated rather than presented as complete: an operator
// deciding from a partial file list must know it is partial.
func TestDiffTask_StatesWhenTheChangeSetOutgrewTheRead(t *testing.T) {
	stub := &taskDiffStub{view: TaskDiffView{
		BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40),
		FileListTruncated: true,
	}}
	queries, _ := diffQueryFixture(t, stub)

	view, err := queries.DiffTask(context.Background(), "task-0001")
	if err != nil {
		t.Fatalf("DiffTask() error = %v", err)
	}
	if !view.FileListTruncated {
		t.Fatal("the view dropped the truncation the inspector reported")
	}
}

// Every way this read can fail to establish ground truth is a failure, never an
// empty diff: "nothing changed" and "nobody could look" are different answers
// and only one of them may be rendered as an empty change set.
func TestDiffTask_RefusesWhenGroundTruthIsUnavailable(t *testing.T) {
	stub := &taskDiffStub{}
	queries, repository := diffQueryFixture(t, stub)

	if _, err := queries.DiffTask(context.Background(), "not a handle"); err == nil {
		t.Error("DiffTask(invalid handle) error = nil")
	}
	if _, err := queries.DiffTask(context.Background(), "task-absent"); err == nil {
		t.Error("DiffTask(absent task) error = nil")
	}
	if len(stub.requests) != 0 {
		t.Fatalf("a refused read reached the inspector: %#v", stub.requests)
	}

	repository.preparationErr = errors.New("preparation unavailable")
	if _, err := queries.DiffTask(context.Background(), "task-0001"); err == nil {
		t.Error("DiffTask(no workspace root) error = nil")
	}
	repository.preparationErr = nil

	failing := &taskDiffStub{err: errors.New("worktree unverified")}
	inspectorFailure, _ := diffQueryFixture(t, failing)
	if _, err := inspectorFailure.DiffTask(context.Background(), "task-0001"); err == nil {
		t.Error("DiffTask(inspector failure) error = nil")
	}

	unconfigured, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: []domain.Task{diffTaskFixture()}, stateVersion: 3},
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := unconfigured.DiffTask(context.Background(), "task-0001"); err == nil {
		t.Error("DiffTask(no inspector) error = nil")
	}
}

// A task with no recorded workspace root has nothing to diff. Reporting an empty
// change set would claim the worker changed nothing.
func TestDiffTask_RefusesATaskWithNoRecordedWorkspace(t *testing.T) {
	stub := &taskDiffStub{}
	repository := &queryRepository{tasks: []domain.Task{diffTaskFixture()}, stateVersion: 3}
	queries, err := NewQueries(QueryConfig{
		Repository: repository, TaskDiffs: stub,
		Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := queries.DiffTask(context.Background(), "task-0001"); err == nil {
		t.Fatal("DiffTask(no workspace root) error = nil")
	}
	if len(stub.requests) != 0 {
		t.Fatalf("a task with no workspace reached the inspector: %#v", stub.requests)
	}
}
