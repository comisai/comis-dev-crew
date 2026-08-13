package sqlite

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type durableTaskCandidateReconciliationStore interface {
	ReadTaskReconciliationAuthority(context.Context, string) (application.TaskReconciliationAuthority, error)
	CommitTaskCandidateReconciliation(context.Context, application.TaskCandidateReconciliationMutation) (application.MutationResult, error)
}

func TestTaskCandidateReconciliation_PersistsExactEvidenceWithoutWorkerReport(t *testing.T) {
	store, task, workspace, now := openUnknownCandidateReconciliationFixture(t, "task-reconcile-clean")
	t.Cleanup(func() { _ = store.Close() })
	reconciliations := any(store).(durableTaskCandidateReconciliationStore)
	authority, err := reconciliations.ReadTaskReconciliationAuthority(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ReadTaskReconciliationAuthority() error = %v", err)
	}
	if !reflect.DeepEqual(authority.Task, task) || authority.PreparationOperationID != "operation-prepare-"+task.Handle ||
		authority.Preparation.RequestedWorkspaceRoot != workspace ||
		authority.TerminalSessionID != "terminal-session-primary" ||
		authority.TerminalTransition != application.TerminalExited ||
		!authority.TerminalObservedAt.Equal(now.Add(5*time.Minute)) {
		t.Fatalf("reconciliation authority = %#v", authority)
	}
	mutation := candidateReconciliationMutation(authority, now, "operation-reconcile-clean")
	result, err := reconciliations.CommitTaskCandidateReconciliation(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskCandidateReconciliation() error = %v", err)
	}
	if result.Task.State != domain.TaskValidating || result.Task.ReportCursor != task.ReportCursor ||
		result.Task.StateVersion != task.StateVersion+2 || result.Operation.ID != mutation.OperationID ||
		result.Operation.Command != "ReconcileTask" || result.Operation.StateVersion != result.Task.StateVersion {
		t.Fatalf("CommitTaskCandidateReconciliation() = %#v", result)
	}
	var reportCount, candidateReportCount int
	if err := store.db.QueryRow(`SELECT COUNT(*), COUNT(CASE WHEN kind = 'candidate_complete' THEN 1 END)
		FROM reports WHERE task_handle = ?`, task.Handle).Scan(&reportCount, &candidateReportCount); err != nil {
		t.Fatalf("read worker reports: %v", err)
	}
	if reportCount != int(task.ReportCursor) || candidateReportCount != 0 {
		t.Fatalf("worker reports = %d/%d, want cursor %d and no synthetic candidate", reportCount, candidateReportCount, task.ReportCursor)
	}
	var startedVersion, completedVersion int64
	var storedHead, storedTerminal string
	if err := store.db.QueryRow(`SELECT started_state_version, completed_state_version,
		head_revision, terminal_transition FROM task_candidate_reconciliations WHERE operation_id = ?`, mutation.OperationID).
		Scan(&startedVersion, &completedVersion, &storedHead, &storedTerminal); err != nil {
		t.Fatalf("read task reconciliation evidence: %v", err)
	}
	if startedVersion != task.StateVersion+1 || completedVersion != task.StateVersion+2 ||
		storedHead != mutation.Snapshot.HeadRevision || storedTerminal != string(application.TerminalExited) {
		t.Fatalf("stored reconciliation = versions %d/%d head %q terminal %q", startedVersion, completedVersion, storedHead, storedTerminal)
	}
	replayed, err := reconciliations.CommitTaskCandidateReconciliation(context.Background(), mutation)
	if err != nil || replayed.Operation != result.Operation || replayed.Task.State != domain.TaskValidating {
		t.Fatalf("CommitTaskCandidateReconciliation(replay) = %#v, %v", replayed, err)
	}
	altered := mutation
	altered.SubjectDigest = strings.Repeat("e", 64)
	if _, err := reconciliations.CommitTaskCandidateReconciliation(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitTaskCandidateReconciliation(altered replay) error = %v, want conflict", err)
	}
}

func TestTaskRecoveryEvidence_DistinguishesSettledCandidateGapFromRestartAmbiguity(t *testing.T) {
	store, task, _, _ := openUnknownCandidateReconciliationFixture(t, "task-recovery-evidence")
	t.Cleanup(func() { _ = store.Close() })
	evidence, err := store.ReadTaskRecoveryEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Kind != application.RecoveryTerminalSettledWithoutCandidate ||
		evidence.Authority.Task.Handle != task.Handle || evidence.Authority.TerminalTransition != application.TerminalExited {
		t.Fatalf("ReadTaskRecoveryEvidence(settled) = %#v, %v", evidence, err)
	}
	if _, err := store.db.Exec("UPDATE task_terminal_bindings SET latest_transition = 'running' WHERE task_handle = ?", task.Handle); err != nil {
		t.Fatal(err)
	}
	evidence, err = store.ReadTaskRecoveryEvidence(context.Background(), task.Handle)
	if err != nil || evidence.Kind != application.RecoveryRestartEvidenceUnresolved ||
		!reflect.DeepEqual(evidence.Authority, application.TaskReconciliationAuthority{}) {
		t.Fatalf("ReadTaskRecoveryEvidence(running) = %#v, %v", evidence, err)
	}
}

func TestTaskRecoveryEvidence_RefusesInvalidOrContradictoryAuthority(t *testing.T) {
	store, task, _, _ := openUnknownCandidateReconciliationFixture(t, "task-recovery-refusal")
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.ReadTaskRecoveryEvidence(context.Background(), "../invalid"); err == nil {
		t.Fatal("ReadTaskRecoveryEvidence(invalid handle) error = nil")
	}
	if _, err := store.ReadTaskRecoveryEvidence(context.Background(), "task-recovery-missing"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadTaskRecoveryEvidence(missing) error = %v, want not found", err)
	}
	if _, err := store.db.Exec("UPDATE tasks SET state = 'working' WHERE handle = ?", task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskRecoveryEvidence(context.Background(), task.Handle); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReadTaskRecoveryEvidence(non-unknown) error = %v, want precondition", err)
	}
}

func TestTaskRecoveryEvidence_TreatsCandidateReportAndAmbiguousPreparationAsUnresolved(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store, domain.Task)
	}{
		{name: "worker candidate exists", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO reports(
				task_handle, local_report_id, subject_digest, schema_version, brief_revision,
				brief_revision_hash, kind, external_key, summary, details, state_version, accepted_at)
				VALUES (?, 'candidate-recovery-existing', ?, 1, ?, ?, 'candidate_complete', '',
				'Candidate exists.', '', 999, ?)`, task.Handle, strings.Repeat("e", 64),
				task.BriefRevision, task.BriefRevisionHash, formatTime(task.UpdatedAt))
		}},
		{name: "preparation operation is ambiguous", mutate: func(store *Store, task domain.Task) {
			operation := storeOperation("operation-prepare-recovery-second", task.StateVersion)
			operation.ResultRef = task.Handle
			_ = store.RecordOperation(context.Background(), operation)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, task, _, _ := openUnknownCandidateReconciliationFixture(t, "task-recovery-unresolved")
			t.Cleanup(func() { _ = store.Close() })
			test.mutate(store, task)
			evidence, err := store.ReadTaskRecoveryEvidence(context.Background(), task.Handle)
			if err != nil || evidence.Kind != application.RecoveryRestartEvidenceUnresolved {
				t.Fatalf("ReadTaskRecoveryEvidence() = %#v, %v", evidence, err)
			}
		})
	}
}

func TestTaskCandidateReconciliation_RefusesChangedAuthorityAndUnsafeCandidate(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store, *application.TaskCandidateReconciliationMutation)
	}{
		{name: "task state differs", mutate: func(store *Store, mutation *application.TaskCandidateReconciliationMutation) {
			_, _ = store.db.Exec("UPDATE tasks SET state = 'working' WHERE handle = ?", mutation.TaskHandle)
		}},
		{name: "task version differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.ExpectedTaskVersion++
		}},
		{name: "preparation operation differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.PreparationOperationID = "operation-prepare-other"
		}},
		{name: "workspace path differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.Snapshot.WorktreePath += "-other"
		}},
		{name: "repository differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.Snapshot.RepositoryID = "other-product"
		}},
		{name: "worktree is dirty", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.Snapshot.Cleanliness = application.WorkspaceDirty
		}},
		{name: "candidate head is base", mutate: func(store *Store, mutation *application.TaskCandidateReconciliationMutation) {
			task, _ := store.GetTask(context.Background(), mutation.TaskHandle)
			mutation.Snapshot.HeadRevision = task.BaseRevision
		}},
		{name: "terminal session differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.TerminalSessionID = "terminal-session-other"
		}},
		{name: "terminal transition differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.TerminalTransition = application.TerminalReleased
		}},
		{name: "terminal observation differs", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.TerminalObservedAt = mutation.TerminalObservedAt.Add(time.Second)
		}},
		{name: "terminal becomes active", mutate: func(store *Store, mutation *application.TaskCandidateReconciliationMutation) {
			_, _ = store.db.Exec("UPDATE task_terminal_bindings SET latest_transition = 'running' WHERE task_handle = ?", mutation.TaskHandle)
		}},
		{name: "validation process is active", mutate: func(store *Store, mutation *application.TaskCandidateReconciliationMutation) {
			_, _ = store.db.Exec(`INSERT INTO validation_processes(
				operation_id, task_handle, program_id, executable_label, pid,
				start_identity, process_group_identity, state, started_at, observed_at)
				VALUES ('validation-reconcile-active', ?, 'go-test', 'go', 123,
				'start-123', 'group-123', 'running', ?, ?)`, mutation.TaskHandle,
				formatTime(mutation.At.Add(-time.Minute)), formatTime(mutation.At.Add(-time.Minute)))
		}},
		{name: "decision remains unresolved", mutate: func(store *Store, mutation *application.TaskCandidateReconciliationMutation) {
			task, _ := store.GetTask(context.Background(), mutation.TaskHandle)
			_, _ = store.db.Exec(`INSERT INTO reports(
				task_handle, local_report_id, subject_digest, schema_version, brief_revision,
				brief_revision_hash, kind, external_key, summary, details, state_version, accepted_at)
				VALUES (?, 'decision-reconcile-open', ?, 1, ?, ?, 'decision', 'decision-reconcile',
				'A bounded decision is required.', '', 999, ?)`, task.Handle, strings.Repeat("e", 64),
				task.BriefRevision, task.BriefRevisionHash, formatTime(task.UpdatedAt))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, task, _, now := openUnknownCandidateReconciliationFixture(t, "task-reconcile-refusal")
			t.Cleanup(func() { _ = store.Close() })
			authority, err := store.ReadTaskReconciliationAuthority(context.Background(), task.Handle)
			if err != nil {
				t.Fatal(err)
			}
			mutation := candidateReconciliationMutation(authority, now, "operation-reconcile-refusal")
			test.mutate(store, &mutation)
			if _, err := store.CommitTaskCandidateReconciliation(context.Background(), mutation); !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("CommitTaskCandidateReconciliation() error = %v, want precondition", err)
			}
			unchanged, err := store.GetTask(context.Background(), task.Handle)
			if err != nil || (unchanged.State != domain.TaskUnknown && test.name != "task state differs") ||
				unchanged.ReportCursor != task.ReportCursor {
				t.Fatalf("task after refusal = %#v, %v", unchanged, err)
			}
		})
	}
}

func TestTaskCandidateReconciliation_FailsAtomicallyAcrossStorageBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Store, *application.TaskCandidateReconciliationMutation)
	}{
		{name: "invalid mutation", mutate: func(_ *Store, mutation *application.TaskCandidateReconciliationMutation) {
			mutation.Action = application.ReconcileTaskAction("invented")
		}},
		{name: "state version exhausted", mutate: func(store *Store, _ *application.TaskCandidateReconciliationMutation) {
			_ = store.RecordOperation(context.Background(), storeOperation("operation-reconcile-max", math.MaxInt64))
		}},
		{name: "task update failure", mutate: func(store *Store, _ *application.TaskCandidateReconciliationMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_reconcile_task_update BEFORE UPDATE ON tasks
				BEGIN SELECT RAISE(FAIL, 'task update unavailable'); END`)
		}},
		{name: "operation insert failure", mutate: func(store *Store, _ *application.TaskCandidateReconciliationMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_reconcile_operation_insert BEFORE INSERT ON operations
				BEGIN SELECT RAISE(FAIL, 'operation insert unavailable'); END`)
		}},
		{name: "evidence insert failure", mutate: func(store *Store, _ *application.TaskCandidateReconciliationMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_reconcile_evidence_insert BEFORE INSERT ON task_candidate_reconciliations
				BEGIN SELECT RAISE(FAIL, 'reconciliation evidence unavailable'); END`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, task, _, now := openUnknownCandidateReconciliationFixture(t, "task-reconcile-atomic")
			t.Cleanup(func() { _ = store.Close() })
			authority, err := store.ReadTaskReconciliationAuthority(context.Background(), task.Handle)
			if err != nil {
				t.Fatal(err)
			}
			mutation := candidateReconciliationMutation(authority, now, "operation-reconcile-atomic")
			test.mutate(store, &mutation)
			if _, err := store.CommitTaskCandidateReconciliation(context.Background(), mutation); err == nil {
				t.Fatal("CommitTaskCandidateReconciliation() error = nil")
			}
			unchanged, readErr := store.GetTask(context.Background(), task.Handle)
			if readErr != nil || unchanged.State != domain.TaskUnknown || unchanged.ReportCursor != task.ReportCursor {
				t.Fatalf("task after atomic failure = %#v, %v", unchanged, readErr)
			}
		})
	}
}

func candidateReconciliationMutation(
	authority application.TaskReconciliationAuthority,
	now time.Time,
	operationID string,
) application.TaskCandidateReconciliationMutation {
	return application.TaskCandidateReconciliationMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: authority.Task.Handle, Action: application.ReconcileValidateCleanCandidate,
		PreparationOperationID: authority.PreparationOperationID,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: authority.Task.Handle, RepositoryID: authority.Task.RepositoryID,
			WorktreePath: authority.Preparation.RequestedWorkspaceRoot,
			Branch:       "devcrew/" + authority.Task.Handle + "-aaaaaaaaaaaaaaaaaaaaaaaa",
			HeadRevision: strings.Repeat("b", 40), Cleanliness: application.WorkspaceClean,
		},
		TerminalSessionID: authority.TerminalSessionID, TerminalTransition: authority.TerminalTransition,
		TerminalObservedAt:  authority.TerminalObservedAt,
		ExpectedTaskVersion: authority.Task.StateVersion, At: now.Add(6 * time.Minute),
	}
}

func openUnknownCandidateReconciliationFixture(
	t *testing.T,
	handle string,
) (*Store, domain.Task, string, time.Time) {
	t.Helper()
	store, task, workspace, now := openTerminalLifecycleFixture(t, handle, true)
	ctx := context.Background()
	if _, err := store.CommitTerminalEvent(ctx, terminalEventMutation(
		task, "operation-running-"+handle, application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(ctx, application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-ack-" + handle, SubjectDigest: strings.Repeat("f", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	client := reportClient(t, store, task, now.Add(4*time.Minute))
	if _, err := client.Report(ctx, sqliteWorkerReport(task, "report-progress-"+handle, domain.ReportProgress)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitTerminalEvent(ctx, terminalEventMutation(
		task, "operation-exited-"+handle, application.TerminalExited, now.Add(5*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	unknown, err := store.GetTask(ctx, handle)
	if err != nil || unknown.State != domain.TaskUnknown || unknown.ReportCursor != 1 {
		t.Fatalf("unknown candidate fixture = %#v, %v", unknown, err)
	}
	return store, unknown, workspace, now
}
