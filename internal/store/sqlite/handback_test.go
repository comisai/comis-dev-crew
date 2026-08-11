package sqlite

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type durableHandbackStore interface {
	CommitTaskHandback(context.Context, application.TaskHandbackMutation) (application.MutationResult, error)
}

func TestTaskHandback_PersistsFreshSnapshotAndStartsDeveloperWorkValidation(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-handback-clean")
	t.Cleanup(func() { _ = store.Close() })
	mutation := application.TaskHandbackMutation{
		OperationID: "operation-handback-clean", SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: task.Handle, Action: application.HandbackValidateDeveloperWork,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: workspace,
			Branch: "devcrew/task-handback-clean", HeadRevision: strings.Repeat("b", 40),
			Cleanliness: application.WorkspaceClean,
		},
		CandidateReport:       handbackCandidateReport(task, "operation-handback-clean"),
		CandidateReportDigest: strings.Repeat("a", 64),
		At:                    now.Add(7 * time.Minute),
	}
	handbacks := any(store).(durableHandbackStore)
	result, err := handbacks.CommitTaskHandback(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskHandback() error = %v", err)
	}
	if result.Task.State != domain.TaskValidating || result.Task.ReportCursor != task.ReportCursor+1 ||
		result.Operation.ID != mutation.OperationID {
		t.Fatalf("CommitTaskHandback() = %#v", result)
	}
	var reportKind domain.WorkerReportKind
	var reportDelivered *string
	if err := store.db.QueryRow(`SELECT reports.kind, outbox.delivered_at
        FROM reports JOIN comis_report_outbox AS outbox
          ON outbox.task_handle = reports.task_handle
          AND outbox.local_report_id = reports.local_report_id
        WHERE reports.task_handle = ? AND reports.local_report_id = ?`, task.Handle, mutation.OperationID).
		Scan(&reportKind, &reportDelivered); err != nil {
		t.Fatalf("read handback candidate report: %v", err)
	}
	if reportKind != domain.ReportCandidateComplete || reportDelivered != nil {
		t.Fatalf("handback candidate report = %q/%v", reportKind, reportDelivered)
	}
	replay, err := handbacks.CommitTaskHandback(context.Background(), mutation)
	if err != nil || replay.Task.State != domain.TaskValidating || replay.Operation != result.Operation {
		t.Fatalf("CommitTaskHandback(replay) = %#v, %v", replay, err)
	}
	var storedHead, storedCleanliness string
	if err := store.db.QueryRow(`SELECT head_revision, cleanliness FROM task_handbacks WHERE operation_id = ?`, mutation.OperationID).
		Scan(&storedHead, &storedCleanliness); err != nil {
		t.Fatalf("read task handback: %v", err)
	}
	if storedHead != mutation.Snapshot.HeadRevision || storedCleanliness != string(application.WorkspaceClean) {
		t.Fatalf("stored handback = %q/%q", storedHead, storedCleanliness)
	}
}

func TestTaskHandback_RefusesLiveTerminalAndAlteredWorkspaceAuthority(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-handback-refusal")
	t.Cleanup(func() { _ = store.Close() })
	base := application.TaskHandbackMutation{
		OperationID: "operation-handback-refusal", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, Action: application.HandbackValidateDeveloperWork,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: workspace,
			Branch: "devcrew/task-handback-refusal", HeadRevision: strings.Repeat("c", 40),
			Cleanliness: application.WorkspaceDirty,
		},
		CandidateReport:       handbackCandidateReport(task, "operation-handback-refusal"),
		CandidateReportDigest: strings.Repeat("b", 64),
		At:                    now.Add(7 * time.Minute),
	}
	if _, err := store.db.Exec(`UPDATE task_terminal_bindings SET latest_transition = 'running' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitTaskHandback(context.Background(), base); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskHandback(live terminal) error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE task_terminal_bindings SET latest_transition = 'exited' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatal(err)
	}
	altered := base
	altered.OperationID = "operation-handback-altered"
	altered.CandidateReport.LocalReportID = altered.OperationID
	altered.Snapshot.WorktreePath = workspace + "-other"
	if _, err := store.CommitTaskHandback(context.Background(), altered); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskHandback(altered workspace) error = %v", err)
	}
	invalidReport := base
	invalidReport.OperationID = "operation-handback-invalid-report"
	invalidReport.CandidateReport.LocalReportID = invalidReport.OperationID
	invalidReport.CandidateReport.Kind = domain.ReportProgress
	if _, err := store.CommitTaskHandback(context.Background(), invalidReport); err == nil {
		t.Fatal("CommitTaskHandback(invalid candidate report) error = nil")
	}
	unchanged, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || unchanged.State != domain.TaskPaused || unchanged.ReportCursor != task.ReportCursor {
		t.Fatalf("task after refused handback = %#v, %v", unchanged, err)
	}
}

func TestTaskHandback_RejectsReplayConflictsAndUnavailableDurableDependencies(t *testing.T) {
	store, task, workspace, now := openPausedHandbackFixture(t, "task-handback-replay")
	mutation := handbackMutation(task, workspace, now, "operation-handback-replay")
	if _, err := store.CommitTaskHandback(context.Background(), mutation); err != nil {
		t.Fatalf("CommitTaskHandback() error = %v", err)
	}
	altered := mutation
	altered.SubjectDigest = strings.Repeat("e", 64)
	if _, err := store.CommitTaskHandback(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitTaskHandback(altered replay) error = %v, want ErrConflict", err)
	}

	for _, test := range []struct {
		name   string
		mutate func(*Store, *application.TaskHandbackMutation)
	}{
		{name: "unknown task", mutate: func(_ *Store, mutation *application.TaskHandbackMutation) {
			mutation.OperationID = "operation-handback-unknown"
			mutation.TaskHandle = "task-handback-unknown"
			mutation.Snapshot.TaskHandle = mutation.TaskHandle
			mutation.CandidateReport.LocalReportID = mutation.OperationID
		}},
		{name: "missing preparation table", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec("ALTER TABLE task_preparations RENAME TO unavailable_task_preparations")
		}},
		{name: "missing terminal table", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec("ALTER TABLE task_terminal_bindings RENAME TO unavailable_terminal_bindings")
		}},
		{name: "missing validation table", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec("ALTER TABLE validation_processes RENAME TO unavailable_validation_processes")
		}},
		{name: "active validation process", mutate: func(store *Store, mutation *application.TaskHandbackMutation) {
			_, _ = store.db.Exec(`INSERT INTO validation_processes(
                    operation_id, task_handle, program_id, executable_label, pid,
                    start_identity, process_group_identity, state, started_at, observed_at)
                VALUES ('validate-handback-active', ?, 'go-test', 'go', 123,
                    'start-123', 'group-123', 'running', ?, ?)`, mutation.TaskHandle,
				formatTime(mutation.At.Add(-time.Minute)), formatTime(mutation.At.Add(-time.Minute)))
		}},
		{name: "candidate brief mismatch", mutate: func(_ *Store, mutation *application.TaskHandbackMutation) {
			mutation.CandidateReport.BriefRevision++
		}},
		{name: "exhausted state version", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			operation := storeOperation("operation-handback-max-version", math.MaxInt64)
			if err := store.RecordOperation(context.Background(), operation); err != nil {
				t.Fatalf("RecordOperation(max version) error = %v", err)
			}
		}},
		{name: "task update failure", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_handback_task_update BEFORE UPDATE ON tasks
                    BEGIN SELECT RAISE(FAIL, 'task update unavailable'); END`)
		}},
		{name: "report insert failure", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_handback_report_insert BEFORE INSERT ON reports
                    BEGIN SELECT RAISE(FAIL, 'report insert unavailable'); END`)
		}},
		{name: "outbox insert failure", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_handback_outbox_insert BEFORE INSERT ON comis_report_outbox
                    BEGIN SELECT RAISE(FAIL, 'outbox insert unavailable'); END`)
		}},
		{name: "operation insert failure", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_handback_operation_insert BEFORE INSERT ON operations
                    BEGIN SELECT RAISE(FAIL, 'operation insert unavailable'); END`)
		}},
		{name: "snapshot insert failure", mutate: func(store *Store, _ *application.TaskHandbackMutation) {
			_, _ = store.db.Exec(`CREATE TRIGGER fail_handback_snapshot_insert BEFORE INSERT ON task_handbacks
                    BEGIN SELECT RAISE(FAIL, 'snapshot insert unavailable'); END`)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, task, workspace, now := openPausedHandbackFixture(t, "task-handback-failure")
			t.Cleanup(func() { _ = store.Close() })
			mutation := handbackMutation(task, workspace, now, "operation-handback-failure")
			test.mutate(store, &mutation)
			if _, err := store.CommitTaskHandback(context.Background(), mutation); err == nil {
				t.Fatal("CommitTaskHandback(failure) error = nil")
			}
		})
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	closedMutation := handbackMutation(task, workspace, now, "operation-handback-closed")
	if _, err := store.CommitTaskHandback(context.Background(), closedMutation); err == nil {
		t.Fatal("CommitTaskHandback(closed) error = nil")
	}
}

func handbackMutation(
	task domain.Task,
	workspace string,
	now time.Time,
	operationID string,
) application.TaskHandbackMutation {
	return application.TaskHandbackMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("d", 64),
		TaskHandle: task.Handle, Action: application.HandbackValidateDeveloperWork,
		Snapshot: application.WorkspaceSnapshot{
			TaskHandle: task.Handle, RepositoryID: task.RepositoryID, WorktreePath: workspace,
			Branch: "devcrew/" + task.Handle, HeadRevision: strings.Repeat("b", 40),
			Cleanliness: application.WorkspaceClean,
		},
		CandidateReport:       handbackCandidateReport(task, operationID),
		CandidateReportDigest: strings.Repeat("a", 64),
		At:                    now.Add(7 * time.Minute),
	}
}

func handbackCandidateReport(task domain.Task, operationID string) domain.WorkerReport {
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: operationID,
		BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Kind:    domain.ReportCandidateComplete,
		Summary: "Developer work was handed back for service validation.",
	}
}

func openPausedHandbackFixture(t *testing.T, handle string) (*Store, domain.Task, string, time.Time) {
	t.Helper()
	store, task, workspace, now := openTerminalLifecycleFixture(t, handle, true)
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-running-"+handle, application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-ack-" + handle, SubjectDigest: strings.Repeat("f", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	client := reportClient(t, store, task, now.Add(4*time.Minute))
	if _, err := client.Report(context.Background(), sqliteWorkerReport(task, "report-paused-"+handle, domain.ReportPaused)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-exited-"+handle, application.TerminalExited, now.Add(5*time.Minute),
	)); err != nil {
		t.Fatal(err)
	}
	paused, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || paused.State != domain.TaskPaused {
		t.Fatalf("paused task = %#v, %v", paused, err)
	}
	return store, paused, workspace, now
}
