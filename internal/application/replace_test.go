package application

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func replaceFixture(
	t *testing.T,
	state domain.TaskState,
	profiles WorkerProfileValidator,
) (*Interventions, *interventionStore) {
	t.Helper()
	interventions, store := resumeFixture(t, state, WorkspaceClean)
	interventions.workerProfiles = profiles
	return interventions, store
}

func validReplacement() ReplaceWorkerCommand {
	return ReplaceWorkerCommand{
		OperationID: "operation-replace-0001", TaskHandle: "task-resume-application",
		WorkerProfileID: "codex-reviewed",
	}
}

// A replacement launches a worker. If it could name any profile string, the
// command whose stated purpose is recovery would become a way to run an
// unreviewed executable.
func TestInterventions_ReplaceRefusesAProfileNoOperatorReviewed(t *testing.T) {
	interventions, store := replaceFixture(t, domain.TaskPaused,
		func(string, domain.TaskShape) error { return ErrNotFound })

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err == nil {
		t.Fatal("ReplaceWorker(unreviewed profile) error = nil, want a refusal")
	}
	if store.replaceCalls != 0 {
		t.Error("a refused replacement must not reach the durable layer")
	}
}

// The profile is checked for this task's own shape. A ship profile taking over a
// scout would hand an investigation the delivery authority the scout shape
// deliberately withholds.
func TestInterventions_ReplaceChecksTheProfileAgainstTheTasksOwnShape(t *testing.T) {
	var checkedShape domain.TaskShape
	interventions, _ := replaceFixture(t, domain.TaskPaused,
		func(_ string, shape domain.TaskShape) error {
			checkedShape = shape
			return nil
		})

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err != nil {
		t.Fatalf("ReplaceWorker() error = %v", err)
	}
	if checkedShape == "" {
		t.Fatal("the replacement profile must be checked against a task shape")
	}
}

// Replacement preserves the work. The snapshot the caller proved travels to the
// durable layer so the recorded trail names the exact tree the new worker
// inherits, rather than leaving it to be re-derived later from a tree that may
// have moved on.
func TestInterventions_ReplacePreservesTheWorkAndRecordsTheTreeInherited(t *testing.T) {
	interventions, store := replaceFixture(t, domain.TaskPaused,
		func(string, domain.TaskShape) error { return nil })

	result, err := interventions.ReplaceWorker(context.Background(), validReplacement())
	if err != nil {
		t.Fatalf("ReplaceWorker() error = %v", err)
	}
	if store.replaceCalls != 1 {
		t.Fatalf("replacements committed = %d, want 1", store.replaceCalls)
	}
	if store.replace.WorkerProfileID != "codex-reviewed" {
		t.Errorf("replacement profile = %q", store.replace.WorkerProfileID)
	}
	if store.replace.Snapshot.HeadRevision != strings.Repeat("b", 40) {
		t.Errorf("recorded head = %q, want the inspected head", store.replace.Snapshot.HeadRevision)
	}
	if result.Task.State != domain.TaskReady {
		t.Errorf("replaced task state = %q, want ready for a fresh launch", result.Task.State)
	}
	// A new brief revision is what makes the swap one generation rather than a
	// second worker joining the first.
	if result.Task.BriefRevision <= store.task.BriefRevision {
		t.Errorf("brief revision = %d, want it advanced past %d",
			result.Task.BriefRevision, store.task.BriefRevision)
	}
}

func TestInterventions_ReplaceRefusesATaskThatIsNotPaused(t *testing.T) {
	interventions, store := replaceFixture(t, domain.TaskWorking,
		func(string, domain.TaskShape) error { return nil })

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err == nil {
		t.Fatal("ReplaceWorker(working) error = nil, want a refusal")
	}
	if store.replaceCalls != 0 {
		t.Error("a refused replacement must not reach the durable layer")
	}
}

// A deployment with no reviewed catalog refuses rather than launching whatever
// the caller named.
func TestInterventions_ReplaceRefusesWhenNoReviewedCatalogIsAvailable(t *testing.T) {
	interventions, _ := replaceFixture(t, domain.TaskPaused, nil)

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err == nil {
		t.Fatal("ReplaceWorker() with no catalog error = nil, want a refusal")
	}
}

func TestInterventions_ReplaceReplaysARepeatedRequest(t *testing.T) {
	interventions, store := replaceFixture(t, domain.TaskPaused,
		func(string, domain.TaskShape) error { return nil })
	store.replayFound = true
	store.replay = MutationResult{Task: domain.Task{Handle: "task-resume-application", State: domain.TaskReady}}

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err != nil {
		t.Fatalf("ReplaceWorker() error = %v", err)
	}
	if store.replaceCalls != 0 {
		t.Error("a replayed replacement must not commit a second swap")
	}
}

func TestInterventions_ReplaceRefusesForgedIdentityAndDeadContexts(t *testing.T) {
	interventions, _ := replaceFixture(t, domain.TaskPaused,
		func(string, domain.TaskShape) error { return nil })

	for name, mutate := range map[string]func(*ReplaceWorkerCommand){
		"no operation":   func(c *ReplaceWorkerCommand) { c.OperationID = "" },
		"forged task":    func(c *ReplaceWorkerCommand) { c.TaskHandle = "../../etc" },
		"no profile":     func(c *ReplaceWorkerCommand) { c.WorkerProfileID = "" },
		"forged profile": func(c *ReplaceWorkerCommand) { c.WorkerProfileID = "../../bin/sh" },
	} {
		command := validReplacement()
		mutate(&command)
		if _, err := interventions.ReplaceWorker(context.Background(), command); err == nil {
			t.Errorf("%s: expected the replacement to be refused", name)
		}
	}
	if _, err := interventions.ReplaceWorker(nilResumeContext(), validReplacement()); err == nil {
		t.Error("ReplaceWorker(nil) error = nil")
	}
}

// A durable trail nothing can read is dormant. The swap is surfaced on task
// detail so an operator can answer the question the task row cannot: this brief
// revision is current because a different worker took over, not because the
// contract changed.
func TestQueries_TaskDetailSurfacesTheWorkerSwapItRecorded(t *testing.T) {
	task := queryTask("task-replaced-detail", domain.TaskReady, 9)
	repository := &queryRepository{
		tasks: []domain.Task{task}, stateVersion: task.StateVersion,
		replacementFound: true,
		replacement: TaskReplacementRecord{
			TaskHandle: task.Handle, PreviousWorkerProfileID: "codex-standard",
			WorkerProfileID: "claude-reviewed", HeadRevision: strings.Repeat("b", 40),
			Cleanliness: WorkspaceClean, BriefRevision: 2,
			ObservedAt: task.UpdatedAt, StateVersion: 9,
		},
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}

	detail, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	if detail.Replacement == nil {
		t.Fatal("task detail omitted the recorded worker swap")
	}
	if detail.Replacement.PreviousWorkerProfileID != "codex-standard" ||
		detail.Replacement.WorkerProfileID != "claude-reviewed" {
		t.Errorf("replacement view = %+v", detail.Replacement)
	}
	// The view names what happened, never why: the reason is the operator's and
	// the service must not put words in their mouth.
	encoded, err := json.Marshal(detail.Replacement)
	if err != nil {
		t.Fatalf("marshal replacement view: %v", err)
	}
	if strings.Contains(strings.ToLower(string(encoded)), "reason") {
		t.Errorf("replacement view invented a reason: %s", encoded)
	}
}

// Most tasks were never replaced. Their detail carries no replacement at all
// rather than an empty one, so a reader cannot mistake "never swapped" for
// "swapped, details unknown".
func TestQueries_TaskDetailOmitsAReplacementThatNeverHappened(t *testing.T) {
	task := queryTask("task-unreplaced-detail", domain.TaskWorking, 7)
	repository := &queryRepository{tasks: []domain.Task{task}, stateVersion: task.StateVersion}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}

	detail, err := queries.ShowTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ShowTask() error = %v", err)
	}
	if detail.Replacement != nil {
		t.Errorf("unreplaced task carried a replacement view = %+v", detail.Replacement)
	}
}

// A replacement read that fails must fail the detail. Silently omitting the
// swap would render an unreplaced task and a task whose swap could not be read
// identically, and an operator reading "no replacement" would conclude the work
// is still with its original worker.
func TestQueries_TaskDetailFailsRatherThanHideAnUnreadableReplacement(t *testing.T) {
	task := queryTask("task-replacement-unreadable", domain.TaskReady, 9)
	repository := &queryRepository{
		tasks: []domain.Task{task}, stateVersion: task.StateVersion,
		replacementErr: errors.New("replacement store unavailable"),
	}
	queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := queries.ShowTask(context.Background(), task.Handle); err == nil {
		t.Fatal("ShowTask() with an unreadable replacement error = nil, want a failure")
	}
}

func TestQueries_TaskDetailRefusesAForgedHandle(t *testing.T) {
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{}, Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := queries.ShowTask(context.Background(), "../../etc"); err == nil {
		t.Fatal("ShowTask(forged handle) error = nil, want a refusal")
	}
}

// A replay that cannot be read leaves the replacement uncertain, and the caller
// must hear that rather than a fresh swap being committed under a repeated
// operation identity.
func TestInterventions_ReplaceReportsAnUnreadableReplayAsAFailure(t *testing.T) {
	interventions, store := replaceFixture(t, domain.TaskPaused,
		func(string, domain.TaskShape) error { return nil })
	store.replayErr = errors.New("replay index unavailable")

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err == nil {
		t.Fatal("ReplaceWorker() with an unreadable replay error = nil, want a failure")
	}
	if store.replaceCalls != 0 {
		t.Error("an unreadable replay must not commit a swap")
	}
}

func TestInterventions_ReplaceReportsACommitFailure(t *testing.T) {
	interventions, store := replaceFixture(t, domain.TaskPaused,
		func(string, domain.TaskShape) error { return nil })
	store.commitErr = errors.New("durable swap unavailable")

	if _, err := interventions.ReplaceWorker(context.Background(), validReplacement()); err == nil {
		t.Fatal("ReplaceWorker() with a failing commit error = nil, want a failure")
	}
}
