package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTerminalLifecycle_JoinsAcknowledgementBeforeRunningAndUpdatesOneBinding(t *testing.T) {
	store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-ack-first", true)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	acknowledgement := terminalLaunchAcknowledgement(task, workspace)
	ackMutation := application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-terminal-ack-first", SubjectDigest: strings.Repeat("d", 64),
		Acknowledgement: acknowledgement, At: now.Add(3 * time.Minute),
	}
	acknowledged, err := store.CommitWorkerLaunchAcknowledgement(ctx, ackMutation)
	if err != nil || acknowledged.Task.State != domain.TaskLaunching {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(before running) = %#v, %v", acknowledged, err)
	}
	replayed, err := store.CommitWorkerLaunchAcknowledgement(ctx, ackMutation)
	if err != nil || !reflect.DeepEqual(replayed, acknowledged) {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(replay) = %#v, %v, want %#v", replayed, err, acknowledged)
	}
	altered := ackMutation
	altered.SubjectDigest = strings.Repeat("e", 64)
	if _, err := store.CommitWorkerLaunchAcknowledgement(ctx, altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(altered replay) error = %v, want conflict", err)
	}

	running := terminalEventMutation(task, "operation-terminal-running-after-ack", application.TerminalRunning, now.Add(4*time.Minute))
	working, err := store.CommitTerminalEvent(ctx, running)
	if err != nil || working.Task.State != domain.TaskWorking {
		t.Fatalf("CommitTerminalEvent(running after acknowledgement) = %#v, %v", working, err)
	}
	runningReplay, err := store.CommitTerminalEvent(ctx, running)
	if err != nil || !reflect.DeepEqual(runningReplay, working) {
		t.Fatalf("CommitTerminalEvent(direct replay) = %#v, %v, want %#v", runningReplay, err, working)
	}
	alteredRunning := running
	alteredRunning.SubjectDigest = strings.Repeat("8", 64)
	if _, err := store.CommitTerminalEvent(ctx, alteredRunning); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitTerminalEvent(altered direct replay) error = %v, want conflict", err)
	}
	stuck := terminalEventMutation(task, "operation-terminal-stuck", application.TerminalStuck, now.Add(5*time.Minute))
	unchanged, err := store.CommitTerminalEvent(ctx, stuck)
	if err != nil || unchanged.Task.State != domain.TaskWorking {
		t.Fatalf("CommitTerminalEvent(stuck) = %#v, %v", unchanged, err)
	}
	var bindings int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM task_terminal_bindings WHERE task_handle = ?", task.Handle).Scan(&bindings); err != nil || bindings != 1 {
		t.Fatalf("terminal binding count = %d, %v, want one", bindings, err)
	}

	lost := terminalEventMutation(task, "operation-terminal-lost", application.TerminalLost, now.Add(6*time.Minute))
	unknown, err := store.CommitTerminalEvent(ctx, lost)
	if err != nil || unknown.Task.State != domain.TaskUnknown {
		t.Fatalf("CommitTerminalEvent(lost) = %#v, %v, want unknown", unknown, err)
	}
}

func TestTerminalLifecycle_RejectsInvalidStaleAndCrossSessionEvidence(t *testing.T) {
	store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-rejections", true)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()

	invalidEvent := terminalEventMutation(task, "operation-terminal-invalid", application.TerminalTransition("invented"), now.Add(3*time.Minute))
	if _, err := store.CommitTerminalEvent(ctx, invalidEvent); err == nil {
		t.Fatal("CommitTerminalEvent(invalid transition) error = nil")
	}
	missing := invalidEvent
	missing.OperationID = "operation-terminal-missing"
	missing.Transition = application.TerminalRunning
	missing.ManagedRunID = "managed-run-missing"
	if _, err := store.CommitTerminalEvent(ctx, missing); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTerminalEvent(missing binding) error = %v, want precondition", err)
	}
	stale := terminalEventMutation(task, "operation-terminal-stale", application.TerminalRunning, now)
	if _, err := store.CommitTerminalEvent(ctx, stale); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTerminalEvent(stale) error = %v, want precondition", err)
	}

	first := terminalEventMutation(task, "operation-terminal-first-session", application.TerminalCreated, now.Add(3*time.Minute))
	if _, err := store.CommitTerminalEvent(ctx, first); err != nil {
		t.Fatalf("CommitTerminalEvent(first session) error = %v", err)
	}
	crossSession := terminalEventMutation(task, "operation-terminal-cross-session", application.TerminalRunning, now.Add(4*time.Minute))
	crossSession.TerminalSessionID = "terminal-session-other"
	if _, err := store.CommitTerminalEvent(ctx, crossSession); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTerminalEvent(cross session) error = %v, want precondition", err)
	}

	invalidAck := application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-terminal-invalid-ack", SubjectDigest: strings.Repeat("f", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, "relative/workspace"), At: now.Add(4 * time.Minute),
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(ctx, invalidAck); err == nil {
		t.Fatal("CommitWorkerLaunchAcknowledgement(invalid) error = nil")
	}
	wrongAck := invalidAck
	wrongAck.OperationID = "operation-terminal-wrong-ack"
	wrongAck.Acknowledgement = terminalLaunchAcknowledgement(task, filepath.Join(workspace, "other"))
	if _, err := store.CommitWorkerLaunchAcknowledgement(ctx, wrongAck); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(wrong workspace) error = %v, want precondition", err)
	}
	missingAck := wrongAck
	missingAck.OperationID = "operation-terminal-missing-ack"
	missingAck.Acknowledgement.TaskHandle = "task-terminal-missing"
	if _, err := store.CommitWorkerLaunchAcknowledgement(ctx, missingAck); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(missing task) error = %v, want not found", err)
	}
}

func TestTerminalLifecycle_RejectsAmbiguousAuthorityBindingAndClosedStore(t *testing.T) {
	store, task, _, now := openTerminalLifecycleFixture(t, "task-terminal-ambiguous-one", true)
	duplicate := task
	duplicate.Handle = "task-terminal-ambiguous-two"
	duplicate.StateVersion++
	duplicate, err := duplicate.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision(ambiguous binding) error = %v", err)
	}
	if err := store.CreateTask(context.Background(), duplicate); err != nil {
		t.Fatalf("CreateTask(ambiguous binding) error = %v", err)
	}
	event := terminalEventMutation(task, "operation-terminal-ambiguous", application.TerminalRunning, now.Add(3*time.Minute))
	if _, err := store.CommitTerminalEvent(context.Background(), event); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTerminalEvent(ambiguous binding) error = %v, want precondition", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	event.OperationID = "operation-terminal-closed"
	if _, err := store.CommitTerminalEvent(context.Background(), event); err == nil {
		t.Fatal("CommitTerminalEvent(closed store) error = nil")
	}
	ack := application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-terminal-ack-closed", SubjectDigest: strings.Repeat("a", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, filepath.Join(canonicalTempDir(t), "workspace")),
		At:              now.Add(3 * time.Minute),
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), ack); err == nil {
		t.Fatal("CommitWorkerLaunchAcknowledgement(closed store) error = nil")
	}
}

func TestTerminalLifecycle_DeterministicFixtureAcknowledgesWithoutTerminal(t *testing.T) {
	store, task, workspace, now := openTerminalLifecycleFixtureWithProfile(t, "task-terminal-fixture", true, "fixture-worker")
	t.Cleanup(func() { _ = store.Close() })
	result, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-terminal-fixture-ack", SubjectDigest: strings.Repeat("7", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
	})
	if err != nil || result.Task.State != domain.TaskWorking {
		t.Fatalf("CommitWorkerLaunchAcknowledgement(fixture) = %#v, %v, want working", result, err)
	}
}

func TestTerminalLifecycle_ExpectedExitPreservesSafePausedCustody(t *testing.T) {
	store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-paused-exit", true)
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-terminal-paused-running", application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(running) error = %v", err)
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-terminal-paused-ack", SubjectDigest: strings.Repeat("8", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("CommitWorkerLaunchAcknowledgement() error = %v", err)
	}
	client := reportClient(t, store, task, now.Add(4*time.Minute))
	paused := sqliteWorkerReport(task, "report-terminal-paused", domain.ReportPaused)
	if _, err := client.Report(context.Background(), paused); err != nil {
		t.Fatalf("Report(paused) error = %v", err)
	}

	result, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
		task, "operation-terminal-paused-exit", application.TerminalExited, now.Add(5*time.Minute),
	))
	if err != nil {
		t.Fatalf("CommitTerminalEvent(exited) error = %v", err)
	}
	if result.Task.State != domain.TaskPaused {
		t.Fatalf("terminal exit state = %q, want %q", result.Task.State, domain.TaskPaused)
	}
}

func TestTerminalLifecycle_RestartLossDoesNotReplaceSettledExitEvidence(t *testing.T) {
	store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-settled-restart", true)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	if _, err := store.CommitTerminalEvent(ctx, terminalEventMutation(
		task, "operation-terminal-settled-running", application.TerminalRunning, now.Add(3*time.Minute),
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(running) error = %v", err)
	}
	if _, err := store.CommitWorkerLaunchAcknowledgement(ctx, application.WorkerLaunchAcknowledgementMutation{
		OperationID: "operation-terminal-settled-ack", SubjectDigest: strings.Repeat("9", 64),
		Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("CommitWorkerLaunchAcknowledgement() error = %v", err)
	}
	exitedAt := now.Add(4 * time.Minute)
	if _, err := store.CommitTerminalEvent(ctx, terminalEventMutation(
		task, "operation-terminal-settled-exited", application.TerminalExited, exitedAt,
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(exited) error = %v", err)
	}
	if _, err := store.CommitTerminalEvent(ctx, terminalEventMutation(
		task, "operation-terminal-restart-lost", application.TerminalLost, now.Add(5*time.Minute),
	)); err != nil {
		t.Fatalf("CommitTerminalEvent(lost after exit) error = %v", err)
	}

	binding, found, err := findTerminalBinding(ctx, store.db, task.Handle)
	if err != nil || !found {
		t.Fatalf("findTerminalBinding() = %#v, %t, %v", binding, found, err)
	}
	if binding.latestTransition != application.TerminalExited || !binding.updatedAt.Equal(exitedAt) {
		t.Fatalf("settled binding = transition %q at %s, want exited at %s",
			binding.latestTransition, binding.updatedAt, exitedAt)
	}
	var lostEvents int
	if err := store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_terminal_events
        WHERE task_handle = ? AND transition = 'lost'`, task.Handle).Scan(&lostEvents); err != nil {
		t.Fatalf("count retained loss events: %v", err)
	}
	if lostEvents != 1 {
		t.Fatalf("retained loss events = %d, want 1", lostEvents)
	}
}

func TestTerminalLifecycle_StorageHelpersFailClosed(t *testing.T) {
	t.Run("missing preparation", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task := storeTask("task-terminal-no-preparation", 1)
		task.State = domain.TaskLaunching
		task.ManagedRunID = "managed-run-no-preparation"
		task.WorkspaceLeaseID = "workspace-lease-no-preparation"
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
			OperationID: "operation-terminal-ack-no-preparation", SubjectDigest: strings.Repeat("4", 64),
			Acknowledgement: terminalLaunchAcknowledgement(task, filepath.Join(canonicalTempDir(t), "workspace")),
			At:              task.UpdatedAt.Add(time.Minute),
		}); err == nil {
			t.Fatal("CommitWorkerLaunchAcknowledgement(missing preparation) error = nil")
		}
	})

	t.Run("state version exhaustion", func(t *testing.T) {
		store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-exhausted", true)
		t.Cleanup(func() { _ = store.Close() })
		if err := store.RecordOperation(context.Background(), storeOperation("operation-terminal-exhaust-marker", int64(^uint64(0)>>1))); err != nil {
			t.Fatalf("RecordOperation(exhausted) error = %v", err)
		}
		if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
			task, "operation-terminal-after-exhaustion", application.TerminalCreated, now.Add(3*time.Minute),
		)); err == nil {
			t.Fatal("CommitTerminalEvent(exhausted) error = nil")
		}
		if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
			OperationID: "operation-terminal-ack-after-exhaustion", SubjectDigest: strings.Repeat("3", 64),
			Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(3 * time.Minute),
		}); err == nil {
			t.Fatal("CommitWorkerLaunchAcknowledgement(exhausted) error = nil")
		}
	})

	t.Run("corrupt binding time", func(t *testing.T) {
		store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-corrupt-binding", true)
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
			task, "operation-terminal-corrupt-binding", application.TerminalCreated, now.Add(3*time.Minute),
		)); err != nil {
			t.Fatalf("CommitTerminalEvent() error = %v", err)
		}
		if _, err := store.db.Exec("UPDATE task_terminal_bindings SET updated_at = 'not-a-time' WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatalf("corrupt terminal binding: %v", err)
		}
		if _, _, err := findTerminalBinding(context.Background(), store.db, task.Handle); err == nil {
			t.Fatal("findTerminalBinding(corrupt time) error = nil")
		}
		if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
			task, "operation-terminal-after-corrupt-binding", application.TerminalRunning, now.Add(4*time.Minute),
		)); err == nil {
			t.Fatal("CommitTerminalEvent(corrupt binding) error = nil")
		}
		if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
			OperationID: "operation-terminal-ack-corrupt-binding", SubjectDigest: strings.Repeat("6", 64),
			Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(4 * time.Minute),
		}); err == nil {
			t.Fatal("CommitWorkerLaunchAcknowledgement(corrupt binding) error = nil")
		}
	})

	t.Run("binding authority mismatch", func(t *testing.T) {
		store, task, workspace, now := openTerminalLifecycleFixture(t, "task-terminal-corrupt-authority", true)
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.CommitTerminalEvent(context.Background(), terminalEventMutation(
			task, "operation-terminal-corrupt-authority", application.TerminalCreated, now.Add(3*time.Minute),
		)); err != nil {
			t.Fatalf("CommitTerminalEvent() error = %v", err)
		}
		if _, err := store.db.Exec("UPDATE task_terminal_bindings SET managed_run_id = 'managed-run-corrupt' WHERE task_handle = ?", task.Handle); err != nil {
			t.Fatalf("corrupt terminal authority: %v", err)
		}
		if _, err := store.CommitWorkerLaunchAcknowledgement(context.Background(), application.WorkerLaunchAcknowledgementMutation{
			OperationID: "operation-terminal-ack-corrupt-authority", SubjectDigest: strings.Repeat("5", 64),
			Acknowledgement: terminalLaunchAcknowledgement(task, workspace), At: now.Add(4 * time.Minute),
		}); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("CommitWorkerLaunchAcknowledgement(corrupt authority) error = %v, want precondition", err)
		}
	})

	t.Run("missing update and foreign task", func(t *testing.T) {
		store, _, _, now := openTerminalLifecycleFixture(t, "task-terminal-helper", true)
		t.Cleanup(func() { _ = store.Close() })
		binding := storedTerminalBinding{
			taskHandle: "task-terminal-absent", managedRunID: "managed-run-absent",
			workspaceLeaseID: "workspace-lease-absent", terminalSessionID: "terminal-session-absent",
			latestTransition: application.TerminalCreated, updatedAt: now.Add(3 * time.Minute),
		}
		if err := putTerminalBinding(context.Background(), store.db, binding, true); !errors.Is(err, application.ErrPrecondition) {
			t.Fatalf("putTerminalBinding(missing update) error = %v, want precondition", err)
		}
		if err := putTerminalBinding(context.Background(), store.db, binding, false); !errors.Is(err, application.ErrConflict) {
			t.Fatalf("putTerminalBinding(foreign task) error = %v, want conflict", err)
		}
	})

	t.Run("closed reads and generic storage error", func(t *testing.T) {
		store, task, _, _ := openTerminalLifecycleFixture(t, "task-terminal-helper-closed", true)
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, _, err := findTerminalBinding(context.Background(), store.db, task.Handle); err == nil {
			t.Fatal("findTerminalBinding(closed) error = nil")
		}
		if _, err := hasLaunchAcknowledgement(context.Background(), store.db, task.Handle); err == nil {
			t.Fatal("hasLaunchAcknowledgement(closed) error = nil")
		}
		if _, err := getTaskByBinding(context.Background(), store.db, task.ManagedRunID, task.WorkspaceLeaseID); err == nil {
			t.Fatal("getTaskByBinding(closed) error = nil")
		}
		cause := errors.New("storage unavailable")
		if err := terminalConstraintFailure("terminal helper", cause); !errors.Is(err, cause) {
			t.Fatalf("terminalConstraintFailure(generic) = %v, want wrapped cause", err)
		}
	})
}

func openTerminalLifecycleFixture(t *testing.T, handle string, start bool) (*Store, domain.Task, string, time.Time) {
	t.Helper()
	return openTerminalLifecycleFixtureWithProfile(t, handle, start, "codex-standard")
}

func openTerminalLifecycleFixtureWithProfile(t *testing.T, handle string, start bool, profile string) (*Store, domain.Task, string, time.Time) {
	t.Helper()
	root := canonicalTempDir(t)
	store, err := Open(context.Background(), filepath.Join(root, "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	now := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)
	workspace := filepath.Join(root, "workspace")
	task := storeTask(handle, 1)
	task.WorkerProfileID = profile
	task.CreatedAt, task.UpdatedAt = now, now
	task, err = task.PinBriefRevision()
	if err != nil {
		_ = store.Close()
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	prepared, err := store.CommitPreparedTask(context.Background(), application.PreparedTaskMutation{
		Task: task,
		Preparation: application.ManagedRunPreparation{
			ExternalRunRef: handle, RegistrationNonce: "registration-nonce-" + handle,
			RequestedWorkspaceRoot: workspace,
			RequestedAttachment:    application.PreparedRuntimeAttachment{Kind: application.RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/" + handle + "/attachment.sock"},
			ExpiresAt:              now.Add(time.Hour), State: application.PreparationOpen,
		},
		OperationID: "operation-prepare-" + handle, SubjectDigest: strings.Repeat("a", 64), At: now,
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("CommitPreparedTask() error = %v", err)
	}
	activated, err := store.CommitManagedRunActivation(context.Background(), application.ManagedRunActivationMutation{
		ServiceInstanceID: task.ServiceInstanceID, ExternalRunRef: handle,
		RegistrationNonce:     "registration-nonce-" + handle,
		Binding:               domain.TaskBinding{ManagedRunID: "managed-run-" + handle, WorkspaceLeaseID: "workspace-lease-" + handle},
		ExecutionAttachmentID: "execution-attachment-" + handle,
		AttachmentTargetName:  "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
		OperationID:           "operation-activate-" + handle, SubjectDigest: strings.Repeat("b", 64), At: now.Add(time.Minute),
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("CommitManagedRunActivation() error = %v", err)
	}
	if !start {
		return store, activated.Task, workspace, now
	}
	started, err := store.CommitTaskStart(context.Background(), application.TaskStartMutation{
		TaskHandle: handle, OperationID: "operation-start-" + handle,
		SubjectDigest: strings.Repeat("c", 64), At: now.Add(2 * time.Minute),
	})
	if err != nil {
		_ = store.Close()
		t.Fatalf("CommitTaskStart() error = %v", err)
	}
	_ = prepared
	return store, started.Task, workspace, now
}

func terminalEventMutation(task domain.Task, operationID string, transition application.TerminalTransition, at time.Time) application.TerminalEventMutation {
	return application.TerminalEventMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("9", 64),
		ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
		TerminalSessionID: "terminal-session-primary", Transition: transition, At: at,
	}
}

func terminalLaunchAcknowledgement(task domain.Task, workspace string) application.LaunchAcknowledgement {
	return application.LaunchAcknowledgement{
		TaskHandle: task.Handle, ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
		WorkingDirectory: workspace, BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
	}
}
