package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type repairAuthorityStub struct {
	authority TaskReconciliationAuthority
	err       error
	handles   []string
}

func (stub *repairAuthorityStub) ReadTaskReconciliationAuthority(
	_ context.Context,
	taskHandle string,
) (TaskReconciliationAuthority, error) {
	stub.handles = append(stub.handles, taskHandle)
	return stub.authority, stub.err
}

func repairAuthorityFixture(task domain.Task) TaskReconciliationAuthority {
	return TaskReconciliationAuthority{
		Task: task, PreparationOperationID: "operation-prepare-repair",
		Preparation: ManagedRunPreparation{
			ExternalRunRef: task.Handle, State: PreparationOpen,
			RequestedWorkspaceRoot: "/approved/worktrees/" + task.Handle,
			RegistrationNonce:      "registration-nonce-repair-survey",
			RequestedAttachment: PreparedRuntimeAttachment{
				Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/attachment.sock",
				RelayIdentity: strings.Repeat("ab", 32),
			},
			ExpiresAt: task.CreatedAt.Add(time.Hour),
		},
		TerminalSessionID: "terminal-session-0001", TerminalTransition: TerminalExited,
		TerminalObservedAt: task.CreatedAt.Add(time.Minute),
	}
}

func repairUnknownTask() domain.Task {
	task := queryTask("task-0001", domain.TaskUnknown, 4)
	task.ManagedRunID = "managed-run-0001"
	task.WorkspaceLeaseID = "workspace-lease-0001"
	task.ExecutionAttachmentID = "execution-attachment-0001"
	task.AttachmentTargetName = "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock"
	pinned, err := task.PinBriefRevision()
	if err != nil {
		panic(err)
	}
	return pinned
}

func repairSurveyFixture(
	t *testing.T,
	tasks []domain.Task,
	authority *repairAuthorityStub,
	inspector *queryReconciliationInspector,
) *Queries {
	t.Helper()
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: tasks, stateVersion: 9},
		Repairs:    authority, ReconciliationWorkspaces: inspector,
		Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	return queries
}

func cleanRepairSnapshot(task domain.Task) WorkspaceSnapshot {
	return WorkspaceSnapshot{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: "/approved/worktrees/" + task.Handle, Branch: "devcrew/" + task.Handle,
		HeadRevision: strings.Repeat("b", 40), Cleanliness: WorkspaceClean,
	}
}

// The survey answers which unknown tasks can actually be reconciled. It reports
// and never acts: choosing an action from evidence is the authority the explicit
// per-task command holds, and a survey that reconciled on its own would become a
// second writer of that transition.
func TestSurveyRepairs_NamesTheTasksAReconcileWouldAccept(t *testing.T) {
	task := repairUnknownTask()
	authority := &repairAuthorityStub{authority: repairAuthorityFixture(task)}
	inspector := &queryReconciliationInspector{snapshot: cleanRepairSnapshot(task)}
	queries := repairSurveyFixture(t, []domain.Task{task}, authority, inspector)

	survey, err := queries.SurveyRepairs(context.Background(), "")
	if err != nil {
		t.Fatalf("SurveyRepairs() error = %v", err)
	}
	if survey.SchemaVersion != 1 || survey.StateVersion != 9 {
		t.Fatalf("survey = %#v", survey)
	}
	if len(survey.Tasks) != 1 || survey.Tasks[0].TaskHandle != "task-0001" {
		t.Fatalf("survey tasks = %#v", survey.Tasks)
	}
	if survey.Tasks[0].Posture != RepairReconcilable {
		t.Errorf("posture = %q, want %q", survey.Tasks[0].Posture, RepairReconcilable)
	}
}

// Every way the evidence falls short is its own posture, because the operator's
// next move differs for each: a dirty tree is handed back, a tree with no
// candidate commit gets a replacement worker, and unproven authority is
// preserved rather than touched.
func TestSurveyRepairs_DistinguishesWhyATaskCannotBeReconciled(t *testing.T) {
	task := repairUnknownTask()
	dirty := cleanRepairSnapshot(task)
	dirty.Cleanliness = WorkspaceDirty
	atBase := cleanRepairSnapshot(task)
	atBase.HeadRevision = task.BaseRevision
	elsewhere := cleanRepairSnapshot(task)
	elsewhere.WorktreePath = "/approved/worktrees/somewhere-else"

	for name, testCase := range map[string]struct {
		authority *repairAuthorityStub
		inspector *queryReconciliationInspector
		want      RepairPosture
	}{
		"dirty worktree": {
			&repairAuthorityStub{authority: repairAuthorityFixture(task)},
			&queryReconciliationInspector{snapshot: dirty}, RepairWorktreeDirty,
		},
		"head still at base": {
			&repairAuthorityStub{authority: repairAuthorityFixture(task)},
			&queryReconciliationInspector{snapshot: atBase}, RepairNoCandidate,
		},
		"worktree identity differs": {
			&repairAuthorityStub{authority: repairAuthorityFixture(task)},
			&queryReconciliationInspector{snapshot: elsewhere}, RepairWorkspaceUnverified,
		},
		"worktree unreadable": {
			&repairAuthorityStub{authority: repairAuthorityFixture(task)},
			&queryReconciliationInspector{err: errors.New("worktree is unverified")}, RepairWorkspaceUnverified,
		},
		"authority unreadable": {
			&repairAuthorityStub{err: errors.New("authority unavailable")},
			&queryReconciliationInspector{snapshot: cleanRepairSnapshot(task)}, RepairAuthorityIncomplete,
		},
		"terminal not settled": {
			&repairAuthorityStub{authority: func() TaskReconciliationAuthority {
				incomplete := repairAuthorityFixture(task)
				incomplete.TerminalTransition = TerminalRunning
				return incomplete
			}()},
			&queryReconciliationInspector{snapshot: cleanRepairSnapshot(task)}, RepairAuthorityIncomplete,
		},
	} {
		t.Run(name, func(t *testing.T) {
			queries := repairSurveyFixture(t, []domain.Task{task}, testCase.authority, testCase.inspector)
			survey, err := queries.SurveyRepairs(context.Background(), "")
			if err != nil {
				t.Fatalf("SurveyRepairs() error = %v", err)
			}
			if len(survey.Tasks) != 1 || survey.Tasks[0].Posture != testCase.want {
				t.Fatalf("survey = %#v, want posture %q", survey.Tasks, testCase.want)
			}
			if !survey.Tasks[0].Posture.Valid() {
				t.Errorf("posture %q is not a declared value", survey.Tasks[0].Posture)
			}
		})
	}
}

// Only unknown tasks are surveyed, because that is the only state the reconcile
// command accepts. Listing a working task would offer an action the service
// would refuse.
func TestSurveyRepairs_SurveysOnlyTheStateReconciliationAccepts(t *testing.T) {
	unknown := repairUnknownTask()
	working := queryTask("task-0002", domain.TaskWorking, 5)
	authority := &repairAuthorityStub{authority: repairAuthorityFixture(unknown)}
	inspector := &queryReconciliationInspector{snapshot: cleanRepairSnapshot(unknown)}
	queries := repairSurveyFixture(t, []domain.Task{unknown, working}, authority, inspector)

	survey, err := queries.SurveyRepairs(context.Background(), "")
	if err != nil {
		t.Fatalf("SurveyRepairs() error = %v", err)
	}
	if len(survey.Tasks) != 1 || survey.Tasks[0].TaskHandle != "task-0001" {
		t.Fatalf("survey tasks = %#v, want only the unknown task", survey.Tasks)
	}
	if len(authority.handles) != 1 {
		t.Errorf("authority reads = %v, want only the unknown task", authority.handles)
	}

	scoped, err := queries.SurveyRepairs(context.Background(), "task-0002")
	if err != nil {
		t.Fatalf("SurveyRepairs(scoped) error = %v", err)
	}
	if len(scoped.Tasks) != 0 {
		t.Fatalf("scoping to a working task returned %#v", scoped.Tasks)
	}
}

// An unreadable survey is a failure, never an empty one: "nothing needs repair"
// is the one answer a broken read must not be able to give.
func TestSurveyRepairs_RefusesInvalidReferencesAndUnavailableReads(t *testing.T) {
	task := repairUnknownTask()
	authority := &repairAuthorityStub{authority: repairAuthorityFixture(task)}
	inspector := &queryReconciliationInspector{snapshot: cleanRepairSnapshot(task)}
	queries := repairSurveyFixture(t, []domain.Task{task}, authority, inspector)

	if _, err := queries.SurveyRepairs(context.Background(), "not a handle"); err == nil {
		t.Error("SurveyRepairs(invalid handle) error = nil")
	}

	failing, err := NewQueries(QueryConfig{
		Repository: &queryRepository{readErr: errors.New("durable read failed")},
		Repairs:    authority, ReconciliationWorkspaces: inspector,
		Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := failing.SurveyRepairs(context.Background(), ""); err == nil {
		t.Error("SurveyRepairs(store failure) error = nil")
	}

	unconfigured, err := NewQueries(QueryConfig{
		Repository: &queryRepository{tasks: []domain.Task{task}, stateVersion: 9},
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := unconfigured.SurveyRepairs(context.Background(), ""); err == nil {
		t.Error("SurveyRepairs(no repair authority) error = nil")
	}
}
