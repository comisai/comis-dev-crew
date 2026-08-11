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

func TestTaskCleanupStore_PersistsReleaseBeforeExactRemovalAuthorizationAndCompletion(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, task, sealed := deliveredCleanupFixture(t, databasePath)
	beginAt := task.UpdatedAt.Add(time.Minute)
	mutation := application.TaskCleanupMutation{
		OperationID: "cleanup-task-0001", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, ReleaseOperationID: "release-task-0001",
		ReleasedAt: beginAt, At: beginAt,
	}
	record, err := store.BeginTaskCleanup(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BeginTaskCleanup() error = %v", err)
	}
	if record.Stage != application.CleanupPrepared || record.TaskHandle != task.Handle ||
		record.PreparationOperationID != "prepare-cleanup-0001" || record.ManagedRunID != task.ManagedRunID ||
		record.WorkspaceLeaseID != task.WorkspaceLeaseID || record.WorktreePath != "/approved/worktrees/"+task.Handle ||
		record.HeadRevision != sealed.Bundle().HeadRevision || record.EvidenceDigest != sealed.Digest() ||
		record.PullRequestID != sealed.Bundle().ForgeEvidence.PullRequestID ||
		!reflect.DeepEqual(record.RequiredForgeChecks, []string{"ci/unit"}) {
		t.Fatalf("BeginTaskCleanup() = %#v", record)
	}
	replay, err := store.BeginTaskCleanup(context.Background(), mutation)
	if err != nil || !reflect.DeepEqual(replay, record) {
		t.Fatalf("BeginTaskCleanup(replay) = %#v, %v", replay, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restarted, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })

	snapshot := application.WorkspaceSnapshot{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: record.WorktreePath, Branch: "devcrew/task-cleanup",
		HeadRevision: record.HeadRevision, Cleanliness: application.WorkspaceClean,
	}
	truth := application.PullRequestDeliveryTruth{
		RepositoryID: task.RepositoryID, PullRequestID: record.PullRequestID,
		HeadRevision: record.HeadRevision,
		Checks:       []application.ForgeCheckTruth{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
	}
	receipt := application.ManagedRunReleaseReceipt{
		ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
		Disposition: application.ManagedRunReleaseReapSafe, ReleasedAt: beginAt,
		State: application.ManagedRunReleased,
	}
	hostReleased, err := restarted.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: snapshot, DeliveryTruth: truth, Receipt: receipt, At: beginAt.Add(time.Minute),
	})
	if err != nil || hostReleased.Stage != application.CleanupHostReleased {
		t.Fatalf("RecordTaskCleanupHostRelease() = %#v, %v", hostReleased, err)
	}
	authorized, err := restarted.AuthorizeTaskCleanupRemoval(context.Background(), application.TaskCleanupRemovalAuthorization{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: snapshot, DeliveryTruth: truth, At: beginAt.Add(2 * time.Minute),
	})
	if err != nil || authorized.Stage != application.CleanupRemovalAuthorized || !reflect.DeepEqual(authorized.Snapshot, snapshot) {
		t.Fatalf("AuthorizeTaskCleanupRemoval() = %#v, %v", authorized, err)
	}
	result, err := restarted.CompleteTaskCleanup(context.Background(), application.TaskCleanupCompletion{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest, At: beginAt.Add(3 * time.Minute),
	})
	if err != nil || result.Task.State != domain.TaskCleaned || result.Task.ManagedRunID != "" ||
		result.Task.WorkspaceLeaseID != "" || result.Task.ExecutionAttachmentID != "" {
		t.Fatalf("CompleteTaskCleanup() = %#v, %v", result, err)
	}
	completedReplay, err := restarted.CompleteTaskCleanup(context.Background(), application.TaskCleanupCompletion{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest, At: beginAt.Add(4 * time.Minute),
	})
	if err != nil || completedReplay.Task.State != domain.TaskCleaned {
		t.Fatalf("CompleteTaskCleanup(replay) = %#v, %v", completedReplay, err)
	}
}

func TestTaskCleanupStore_RefusesOpenHoldUnsettledRuntimeAndUndeliveredEvidence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Store, domain.Task)
	}{
		{name: "open cleanup hold", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO task_cleanup_holds(task_handle, hold_id, reason, opened_at)
                VALUES (?, 'hold-review', 'review remains open', ?)`, task.Handle, formatTime(task.UpdatedAt))
		}},
		{name: "running terminal", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("UPDATE task_terminal_bindings SET latest_transition = 'running' WHERE task_handle = ?", task.Handle)
		}},
		{name: "active validation", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO validation_processes(
                    operation_id, task_handle, program_id, executable_label, pid,
                    start_identity, process_group_identity, state, started_at, observed_at)
                VALUES ('validate-cleanup-active', ?, 'go-test', 'go', 123, 'start-123', 'group-123', 'running', ?, ?)`,
				task.Handle, formatTime(task.UpdatedAt), formatTime(task.UpdatedAt))
		}},
		{name: "undelivered evidence", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("UPDATE comis_evidence_outbox SET delivered_at = NULL WHERE task_handle = ? LIMIT 1", task.Handle)
		}},
		{name: "unresolved decision", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec(`INSERT INTO reports(
                    task_handle, local_report_id, subject_digest, schema_version, brief_revision,
                    brief_revision_hash, kind, external_key, summary, details, state_version, accepted_at)
                VALUES (?, 'decision-cleanup-open', ?, 1, ?, ?, 'decision', 'decision-open',
                    'A bounded decision is required.', '', 999, ?)`, task.Handle, strings.Repeat("f", 64),
				task.BriefRevision, task.BriefRevisionHash, formatTime(task.UpdatedAt))
		}},
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
				t.Fatalf("BeginTaskCleanup() error = %v, want ErrPrecondition", err)
			}
		})
	}
}

func deliveredCleanupFixture(t *testing.T, databasePath string) (*Store, domain.Task, *domain.SealedDeliveryEvidence) {
	t.Helper()
	store, task := openReportFixture(t, databasePath)
	t.Cleanup(func() { _ = store.Close() })
	workspace := "/approved/worktrees/" + task.Handle
	if _, err := store.db.Exec(`INSERT INTO task_preparations(
        task_handle, external_run_ref, registration_nonce, expires_at, created_at,
        requested_workspace_root, state, abandon_reason, disposition,
        requested_attachment_kind, requested_attachment_source_path)
        VALUES (?, ?, 'registration-cleanup-0001', ?, ?, ?, 'open', '', '', 'unix_socket', ?)`,
		task.Handle, task.Handle, formatTime(task.CreatedAt.Add(24*time.Hour)), formatTime(task.CreatedAt), workspace,
		"/approved/runtime/"+task.Handle+"/attachment.sock"); err != nil {
		t.Fatalf("insert cleanup preparation: %v", err)
	}
	prepare := storeOperation("prepare-cleanup-0001", 2)
	prepare.ResultRef = task.Handle
	if err := store.RecordOperation(context.Background(), prepare); err != nil {
		t.Fatalf("RecordOperation(prepare) error = %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO task_terminal_bindings(
        task_handle, managed_run_id, workspace_lease_id, terminal_session_id,
        latest_transition, running_observed, updated_at)
        VALUES (?, ?, ?, 'terminal-cleanup-0001', 'exited', 1, ?)`,
		task.Handle, task.ManagedRunID, task.WorkspaceLeaseID, formatTime(task.UpdatedAt)); err != nil {
		t.Fatalf("insert terminal binding: %v", err)
	}
	report := sqliteWorkerReport(task, "report-cleanup-candidate", domain.ReportCandidateComplete)
	if _, err := store.CommitReport(context.Background(), directReportMutation(task, report, task.UpdatedAt.Add(time.Minute))); err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	validating, err := store.GetTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	sealed := candidateEvidence(t, validating, strings.Repeat("b", 40))
	publications := candidateEvidencePublications(t, validating, sealed)
	judgedAt := validating.UpdatedAt.Add(5 * time.Minute)
	if _, _, err := store.CommitCandidateEvidence(context.Background(), validating.Handle, sealed,
		[]string{"unit"}, []string{"ci/unit"}, judgedAt, publications); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	for range publications {
		evidence, found, err := store.NextComisEvidence(context.Background())
		if err != nil || !found {
			t.Fatalf("NextComisEvidence() = %#v, %t, %v", evidence, found, err)
		}
		deliveredAt := judgedAt.Add(time.Minute)
		retainedUntil := deliveredAt.Add(time.Hour)
		if err := store.MarkComisEvidenceDelivered(context.Background(), evidence.OperationID, application.ComisEvidenceAcknowledgement{
			ManagedRunID: evidence.ManagedRunID, EvidenceRef: evidence.EvidenceRef,
			ContentHash: evidence.ContentHash, VerificationLevel: evidence.VerificationLevel,
			RetainedUntil: &retainedUntil,
		}, deliveredAt); err != nil {
			t.Fatal(err)
		}
	}
	pending, found, err := store.NextComisReport(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisReport() = %#v, %t, %v", pending, found, err)
	}
	reportDeliveredAt := judgedAt.Add(2 * time.Minute)
	if err := store.MarkComisReportDelivered(context.Background(), pending.OperationID, application.ComisReportAcknowledgement{
		ManagedRunID: pending.ManagedRunID, ServiceReportID: pending.ServiceReportID,
		AcceptedSequence: 17, RetainedUntil: reportDeliveredAt.Add(time.Hour),
	}, reportDeliveredAt); err != nil {
		t.Fatal(err)
	}
	delivered, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || delivered.State != domain.TaskDelivered {
		t.Fatalf("delivered cleanup fixture = %#v, %v", delivered, err)
	}
	return store, delivered, sealed
}
