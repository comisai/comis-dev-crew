package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// TestAudit_RecordsEveryRefusedCleanupSafetyCheck makes a refused destructive
// operation leave a durable trace.
//
// A refusal changes no task state, so the transition log — which by design
// records transitions and nothing else — never sees it. That is the right rule
// for the transition log and the wrong outcome for a safety check: the fact
// that removal of a worktree was attempted and refused, and on which ground, is
// exactly what an operator reconstructing an incident needs and the one thing
// nothing durable currently keeps.
func TestAudit_RecordsEveryRefusedCleanupSafetyCheck(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Store, domain.Task)
		reason application.AuditReason
	}{
		{name: "open cleanup hold", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO task_cleanup_holds(task_handle, hold_id, reason, opened_at)
                VALUES (?, 'hold-review', 'review remains open', ?)`, task.Handle, formatTime(task.UpdatedAt))
		}, reason: application.AuditCleanupOpenHold},
		{name: "running terminal", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("UPDATE task_terminal_bindings SET latest_transition = 'running' WHERE task_handle = ?", task.Handle)
		}, reason: application.AuditCleanupActiveExecution},
		{name: "lost terminal", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("UPDATE task_terminal_bindings SET latest_transition = 'lost' WHERE task_handle = ?", task.Handle)
		}, reason: application.AuditCleanupUnknownExecution},
		{name: "unresolved decision", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO reports(
                    task_handle, local_report_id, subject_digest, schema_version, brief_revision,
                    brief_revision_hash, kind, external_key, summary, details, state_version, accepted_at)
                VALUES (?, 'decision-cleanup-open', ?, 1, ?, ?, 'decision', 'decision-open',
					'A bounded decision is required.', '', 999, ?)`, task.Handle, strings.Repeat("f", 64),
				task.BriefRevision, task.BriefRevisionHash, formatTime(task.UpdatedAt))
		}, reason: application.AuditCleanupOpenDecision},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
			t.Cleanup(func() { _ = store.Close() })
			test.mutate(store, task)

			_, err := store.BeginTaskCleanup(context.Background(), application.TaskCleanupMutation{
				OperationID: "cleanup-refused-0001", SubjectDigest: strings.Repeat("e", 64),
				TaskHandle: task.Handle, ReleaseOperationID: "release-refused-0001",
				ReleasedAt: task.UpdatedAt.Add(time.Minute), At: task.UpdatedAt.Add(time.Minute),
			})
			if !errors.Is(err, application.ErrPrecondition) {
				t.Fatalf("BeginTaskCleanup() error = %v, want a classified refusal", err)
			}

			events, readErr := store.ReadAuditEvents(context.Background(), 0, 50)
			if readErr != nil {
				t.Fatalf("ReadAuditEvents() error = %v", readErr)
			}
			var found *application.AuditEvent
			for index := range events {
				if events[index].Kind == application.AuditCleanupRefused {
					found = &events[index]
				}
			}
			if found == nil {
				t.Fatalf("a refused cleanup left no audit record; read %d events", len(events))
			}
			if found.TaskHandle != task.Handle {
				t.Errorf("audit task = %q, want %q", found.TaskHandle, task.Handle)
			}
			if found.Reason != test.reason {
				t.Errorf("audit reason = %q, want %q", found.Reason, test.reason)
			}
			if found.Sequence <= 0 || found.OccurredAt.IsZero() {
				t.Errorf("audit identity = %#v", *found)
			}
		})
	}
}

// TestAudit_KeepsTheRefusalWhenTheCleanupTransactionRollsBack is the property
// that makes the record trustworthy: the refused work is undone, the record of
// the refusal is not.
func TestAudit_KeepsTheRefusalWhenTheCleanupTransactionRollsBack(t *testing.T) {
	store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec(`INSERT INTO task_cleanup_holds(task_handle, hold_id, reason, opened_at)
        VALUES (?, 'hold-review', 'review remains open', ?)`, task.Handle, formatTime(task.UpdatedAt)); err != nil {
		t.Fatalf("seed cleanup hold: %v", err)
	}
	if _, err := store.BeginTaskCleanup(context.Background(), application.TaskCleanupMutation{
		OperationID: "cleanup-refused-0002", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, ReleaseOperationID: "release-refused-0002",
		ReleasedAt: task.UpdatedAt.Add(time.Minute), At: task.UpdatedAt.Add(time.Minute),
	}); !errors.Is(err, application.ErrCleanupOpenHold) {
		t.Fatalf("BeginTaskCleanup() error = %v, want an open-hold refusal", err)
	}
	var staged int
	if err := store.db.QueryRow(
		"SELECT COUNT(*) FROM task_cleanup_operations WHERE task_handle = ?", task.Handle,
	).Scan(&staged); err != nil {
		t.Fatalf("count staged cleanup operations: %v", err)
	}
	if staged != 0 {
		t.Errorf("refused cleanup staged %d operations, want 0", staged)
	}
	events, err := store.ReadAuditEvents(context.Background(), 0, 50)
	if err != nil {
		t.Fatalf("ReadAuditEvents() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the rollback took the audit record with it")
	}
}

func TestAudit_BoundsAndResumesReads(t *testing.T) {
	store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	at := task.UpdatedAt.Add(time.Minute)
	for index := 0; index < 3; index++ {
		if err := store.RecordAuditEvent(context.Background(), application.AuditEvent{
			OccurredAt: at, Kind: application.AuditReportAuthenticationFailed,
			TaskHandle: task.Handle, Reason: application.AuditCredentialMismatch,
		}); err != nil {
			t.Fatalf("RecordAuditEvent() error = %v", err)
		}
	}
	first, err := store.ReadAuditEvents(context.Background(), 0, 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("ReadAuditEvents(0, 2) = %d events, %v", len(first), err)
	}
	rest, err := store.ReadAuditEvents(context.Background(), first[len(first)-1].Sequence, 50)
	if err != nil {
		t.Fatalf("ReadAuditEvents(cursor) error = %v", err)
	}
	for _, event := range rest {
		if event.Sequence <= first[len(first)-1].Sequence {
			t.Errorf("cursor returned sequence %d at or before the cursor", event.Sequence)
		}
	}
	if _, err := store.ReadAuditEvents(context.Background(), -1, 10); err == nil {
		t.Error("ReadAuditEvents() accepted a negative cursor")
	}
	if _, err := store.ReadAuditEvents(context.Background(), 0, 0); err == nil {
		t.Error("ReadAuditEvents() accepted an unbounded page")
	}
}

func TestAudit_RejectsUnknownKindsAndReasons(t *testing.T) {
	store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	at := task.UpdatedAt.Add(time.Minute)
	for name, event := range map[string]application.AuditEvent{
		"unknown kind": {OccurredAt: at, Kind: "invented", Reason: application.AuditCredentialMismatch},
		"unknown reason": {
			OccurredAt: at, Kind: application.AuditCleanupRefused, Reason: "invented",
		},
		"absent time": {Kind: application.AuditCleanupRefused, Reason: application.AuditCleanupOpenHold},
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.RecordAuditEvent(context.Background(), event); err == nil {
				t.Fatal("RecordAuditEvent() accepted an invalid record")
			}
		})
	}
}
