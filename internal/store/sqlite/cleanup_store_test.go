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
	laterReplay := mutation
	laterReplay.ReleasedAt = beginAt.Add(time.Minute)
	laterReplay.At = laterReplay.ReleasedAt
	replay, err := store.BeginTaskCleanup(context.Background(), laterReplay)
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
			_, _ = store.db.Exec(`UPDATE comis_evidence_outbox SET delivered_at = NULL
                WHERE operation_id = (SELECT operation_id FROM comis_evidence_outbox
                    WHERE task_handle = ? ORDER BY operation_id LIMIT 1)`, task.Handle)
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

func TestTaskCleanupStore_RejectsAlteredReplaysAndMismatchedProofs(t *testing.T) {
	store, task, sealed := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	beginAt := task.UpdatedAt.Add(time.Minute)
	mutation := application.TaskCleanupMutation{
		OperationID: "cleanup-replay-0001", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, ReleaseOperationID: "release-replay-0001",
		ReleasedAt: beginAt, At: beginAt,
	}
	record, err := store.BeginTaskCleanup(context.Background(), mutation)
	if err != nil {
		t.Fatalf("BeginTaskCleanup() error = %v", err)
	}
	altered := mutation
	altered.ReleaseOperationID = "release-replay-altered"
	if _, err := store.BeginTaskCleanup(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("BeginTaskCleanup(altered) error = %v, want ErrConflict", err)
	}
	snapshot := application.WorkspaceSnapshot{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: record.WorktreePath, Branch: "devcrew/task-cleanup",
		HeadRevision: sealed.Bundle().HeadRevision, Cleanliness: application.WorkspaceClean,
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
	badSnapshot := snapshot
	badSnapshot.HeadRevision = strings.Repeat("c", 40)
	if _, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: badSnapshot, DeliveryTruth: truth, Receipt: receipt, At: beginAt.Add(time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("RecordTaskCleanupHostRelease(bad snapshot) error = %v, want ErrPrecondition", err)
	}
	badReceipt := receipt
	badReceipt.WorkspaceLeaseID = "workspace-lease-altered"
	if _, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: snapshot, DeliveryTruth: truth, Receipt: badReceipt, At: beginAt.Add(time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("RecordTaskCleanupHostRelease(bad receipt) error = %v, want ErrPrecondition", err)
	}
	hostReleased, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: snapshot, DeliveryTruth: truth, Receipt: receipt, At: beginAt.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("RecordTaskCleanupHostRelease() error = %v", err)
	}
	replayed, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: snapshot, DeliveryTruth: truth, Receipt: receipt, At: beginAt.Add(2 * time.Minute),
	})
	if err != nil || !reflect.DeepEqual(replayed, hostReleased) {
		t.Fatalf("RecordTaskCleanupHostRelease(replay) = %#v, %v", replayed, err)
	}
	if _, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest,
		Snapshot: badSnapshot, DeliveryTruth: truth, Receipt: receipt, At: beginAt.Add(2 * time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("RecordTaskCleanupHostRelease(altered proof) error = %v, want ErrPrecondition", err)
	}
	if _, err := store.AuthorizeTaskCleanupRemoval(context.Background(), application.TaskCleanupRemovalAuthorization{
		OperationID: mutation.OperationID, SubjectDigest: strings.Repeat("f", 64),
		Snapshot: snapshot, DeliveryTruth: truth, At: beginAt.Add(2 * time.Minute),
	}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("AuthorizeTaskCleanupRemoval(altered digest) error = %v, want ErrConflict", err)
	}
	if _, err := store.CompleteTaskCleanup(context.Background(), application.TaskCleanupCompletion{
		OperationID: mutation.OperationID, SubjectDigest: mutation.SubjectDigest, At: beginAt.Add(2 * time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CompleteTaskCleanup(early) error = %v, want ErrPrecondition", err)
	}
}

func TestTaskCleanupStore_RejectsInvalidUnknownAndUnavailableRequests(t *testing.T) {
	store, task, sealed := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if _, err := store.BeginTaskCleanup(context.Background(), application.TaskCleanupMutation{}); err == nil {
		t.Fatal("BeginTaskCleanup(invalid) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	at := task.UpdatedAt.Add(time.Minute)
	if _, err := store.BeginTaskCleanup(canceled, application.TaskCleanupMutation{
		OperationID: "cleanup-canceled-0001", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, ReleaseOperationID: "release-canceled-0001", ReleasedAt: at, At: at,
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("BeginTaskCleanup(canceled) error = %v, want context.Canceled", err)
	}
	snapshot := application.WorkspaceSnapshot{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: "/approved/worktrees/" + task.Handle, Branch: "devcrew/task-cleanup",
		HeadRevision: sealed.Bundle().HeadRevision, Cleanliness: application.WorkspaceClean,
	}
	if _, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{}); err == nil {
		t.Fatal("RecordTaskCleanupHostRelease(invalid) error = nil")
	}
	if _, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: "cleanup-unknown-0001", SubjectDigest: strings.Repeat("e", 64),
		Snapshot: snapshot, At: at,
	}); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("RecordTaskCleanupHostRelease(unknown) error = %v, want ErrNotFound", err)
	}
	if _, err := store.CompleteTaskCleanup(context.Background(), application.TaskCleanupCompletion{}); err == nil {
		t.Fatal("CompleteTaskCleanup(invalid) error = nil")
	}
	if _, err := store.CompleteTaskCleanup(context.Background(), application.TaskCleanupCompletion{
		OperationID: "cleanup-unknown-0001", SubjectDigest: strings.Repeat("e", 64), At: at,
	}); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CompleteTaskCleanup(unknown) error = %v, want ErrNotFound", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	valid := application.TaskCleanupMutation{
		OperationID: "cleanup-closed-0001", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, ReleaseOperationID: "release-closed-0001", ReleasedAt: at, At: at,
	}
	if _, err := store.BeginTaskCleanup(context.Background(), valid); err == nil {
		t.Fatal("BeginTaskCleanup(closed) error = nil")
	}
	if _, err := store.RecordTaskCleanupHostRelease(context.Background(), application.TaskCleanupHostReleaseMutation{
		OperationID: valid.OperationID, SubjectDigest: valid.SubjectDigest, Snapshot: snapshot, At: at,
	}); err == nil {
		t.Fatal("RecordTaskCleanupHostRelease(closed) error = nil")
	}
	if _, err := store.CompleteTaskCleanup(context.Background(), application.TaskCleanupCompletion{
		OperationID: valid.OperationID, SubjectDigest: valid.SubjectDigest, At: at,
	}); err == nil {
		t.Fatal("CompleteTaskCleanup(closed) error = nil")
	}
}

func TestTaskCleanupStore_RejectsMissingPreparationCorruptEvidenceAndStaleTime(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Store, domain.Task)
	}{
		{name: "missing completed preparation", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("DELETE FROM operations WHERE result_ref = ?", task.Handle)
		}},
		{name: "ambiguous completed preparation", mutate: func(store *Store, task domain.Task) {
			operation := storeOperation("prepare-cleanup-duplicate", 99)
			operation.ResultRef = task.Handle
			if err := store.RecordOperation(context.Background(), operation); err != nil {
				t.Fatalf("RecordOperation(duplicate preparation) error = %v", err)
			}
		}},
		{name: "corrupt candidate evidence", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("UPDATE candidate_evidence SET canonical = X'00' WHERE task_handle = ?", task.Handle)
		}},
		{name: "candidate judgment differs", mutate: func(store *Store, task domain.Task) {
			_, _ = store.db.Exec("UPDATE candidate_evidence SET reason = 'local_check_failed' WHERE task_handle = ?", task.Handle)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
			test.mutate(store, task)
			at := task.UpdatedAt.Add(time.Minute)
			_, err := store.BeginTaskCleanup(context.Background(), application.TaskCleanupMutation{
				OperationID: "cleanup-safety-0001", SubjectDigest: strings.Repeat("e", 64),
				TaskHandle: task.Handle, ReleaseOperationID: "release-safety-0001", ReleasedAt: at, At: at,
			})
			if !errors.Is(err, application.ErrPrecondition) && err == nil {
				t.Fatal("BeginTaskCleanup(unsafe) error = nil")
			}
		})
	}
	store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "stale.db"))
	stale := task.UpdatedAt.Add(-time.Minute)
	if _, err := store.BeginTaskCleanup(context.Background(), application.TaskCleanupMutation{
		OperationID: "cleanup-stale-0001", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, ReleaseOperationID: "release-stale-0001", ReleasedAt: stale, At: stale,
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("BeginTaskCleanup(stale) error = %v, want ErrPrecondition", err)
	}
}

func TestTaskCleanupProofAndRecordReadersRejectCorruptState(t *testing.T) {
	record := application.TaskCleanupRecord{
		TaskHandle: "task-proof-0001", RepositoryID: "product-api",
		WorktreePath: "/approved/worktrees/task-proof-0001", HeadRevision: strings.Repeat("b", 40),
		ReportArtifactHash: strings.Repeat("a", 64),
	}
	snapshot := application.WorkspaceSnapshot{
		TaskHandle: record.TaskHandle, RepositoryID: record.RepositoryID, WorktreePath: record.WorktreePath,
		Branch: "devcrew/task-proof", HeadRevision: record.HeadRevision, Cleanliness: application.WorkspaceClean,
	}
	if err := validateCleanupProof(record, snapshot, application.PullRequestDeliveryTruth{}); err != nil {
		t.Fatalf("validateCleanupProof(report) error = %v", err)
	}
	if err := validateCleanupProof(record, snapshot, application.PullRequestDeliveryTruth{RepositoryID: record.RepositoryID}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("validateCleanupProof(report mismatch) error = %v, want ErrPrecondition", err)
	}
	badSnapshot := snapshot
	badSnapshot.Cleanliness = application.WorkspaceDirty
	if err := validateCleanupProof(record, badSnapshot, application.PullRequestDeliveryTruth{}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("validateCleanupProof(workspace mismatch) error = %v, want ErrPrecondition", err)
	}
	if cleanupProofMatches(record, snapshot, application.PullRequestDeliveryTruth{}) {
		t.Fatal("cleanupProofMatches(unpersisted snapshot) = true")
	}
	record.Snapshot = snapshot
	if !cleanupProofMatches(record, snapshot, application.PullRequestDeliveryTruth{}) {
		t.Fatal("cleanupProofMatches(report proof) = false")
	}

	for _, test := range []struct {
		name   string
		column string
		value  string
	}{
		{name: "forge checks", column: "required_forge_checks_json", value: "not-json"},
		{name: "release time", column: "released_at", value: "not-a-time"},
		{name: "stage", column: "stage", value: "invented"},
		{name: "snapshot", column: "snapshot_cleanliness", value: "invented"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
			at := task.UpdatedAt.Add(time.Minute)
			mutation := application.TaskCleanupMutation{
				OperationID: "cleanup-corrupt-0001", SubjectDigest: strings.Repeat("e", 64),
				TaskHandle: task.Handle, ReleaseOperationID: "release-corrupt-0001", ReleasedAt: at, At: at,
			}
			if _, err := store.BeginTaskCleanup(context.Background(), mutation); err != nil {
				t.Fatalf("BeginTaskCleanup() error = %v", err)
			}
			query := "UPDATE task_cleanup_operations SET " + test.column + " = ? WHERE operation_id = ?" // #nosec G202 -- column is a closed test fixture.
			if _, err := store.db.Exec(query, test.value, mutation.OperationID); err != nil {
				t.Fatalf("corrupt cleanup record: %v", err)
			}
			if _, err := store.BeginTaskCleanup(context.Background(), mutation); err == nil {
				t.Fatal("BeginTaskCleanup(corrupt replay) error = nil")
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
