package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestComisEvidenceOutbox_PersistsExactPublicationsAndAcknowledgementsAcrossRestart(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := candidateEvidenceTask(t, "task-evidence-outbox")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	publications := candidateEvidencePublications(t, task, sealed)
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	if _, judgment, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("CommitCandidateEvidence() = %#v, %v", judgment, err)
	}
	first, found, err := store.NextComisEvidence(context.Background())
	if err != nil || !found || first.OperationID != publications[0].OperationID ||
		first.ManagedRunID != task.ManagedRunID || string(first.Body) != string(publications[0].Body) {
		t.Fatalf("NextComisEvidence() = %#v, %t, %v", first, found, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	restarted, found, err := reopened.NextComisEvidence(context.Background())
	if err != nil || !found || restarted.OperationID != first.OperationID || string(restarted.Body) != string(first.Body) {
		t.Fatalf("NextComisEvidence(restart) = %#v, %t, %v", restarted, found, err)
	}
	deliveredAt := judgedAt.Add(time.Minute)
	retainedUntil := deliveredAt.Add(24 * time.Hour)
	acknowledgement := application.ComisEvidenceAcknowledgement{
		ManagedRunID: first.ManagedRunID, EvidenceRef: first.EvidenceRef,
		ContentHash: first.ContentHash, VerificationLevel: first.VerificationLevel,
		RetainedUntil: &retainedUntil,
	}
	if err := reopened.MarkComisEvidenceDelivered(
		context.Background(), first.OperationID, acknowledgement, deliveredAt,
	); err != nil {
		t.Fatalf("MarkComisEvidenceDelivered() error = %v", err)
	}
	if err := reopened.MarkComisEvidenceDelivered(
		context.Background(), first.OperationID, acknowledgement, deliveredAt,
	); err != nil {
		t.Fatalf("MarkComisEvidenceDelivered(replay) error = %v", err)
	}
	second, found, err := reopened.NextComisEvidence(context.Background())
	if err != nil || !found || second.OperationID != publications[1].OperationID {
		t.Fatalf("NextComisEvidence(second) = %#v, %t, %v", second, found, err)
	}
	altered := acknowledgement
	altered.ContentHash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	if err := reopened.MarkComisEvidenceDelivered(
		context.Background(), first.OperationID, altered, deliveredAt,
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("MarkComisEvidenceDelivered(altered) error = %v, want ErrConflict", err)
	}
}

func TestComisEvidenceOutbox_CompletesReconciledCandidateWithoutWorkerReport(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "reconciled.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := candidateEvidenceTask(t, "task-reconciled-delivery")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	insertEvidenceReconciliation(t, store, task, sealed.Bundle().HeadRevision)
	publications := candidateEvidencePublications(t, task, sealed)
	judgedAt := sealed.Bundle().ProducedAt
	if _, judgment, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("CommitCandidateEvidence() = %#v, %v", judgment, err)
	}

	for index := range publications {
		delivery, found, err := store.NextComisEvidence(context.Background())
		if err != nil || !found {
			t.Fatalf("NextComisEvidence(%d) = %#v, %t, %v", index, delivery, found, err)
		}
		deliveredAt := judgedAt.Add(time.Duration(index+1) * time.Minute)
		retainedUntil := deliveredAt.Add(time.Hour)
		if err := store.MarkComisEvidenceDelivered(context.Background(), delivery.OperationID, application.ComisEvidenceAcknowledgement{
			ManagedRunID: delivery.ManagedRunID, EvidenceRef: delivery.EvidenceRef,
			ContentHash: delivery.ContentHash, VerificationLevel: delivery.VerificationLevel,
			RetainedUntil: &retainedUntil,
		}, deliveredAt); err != nil {
			t.Fatalf("MarkComisEvidenceDelivered(%d) error = %v", index, err)
		}
		updated, err := store.GetTask(context.Background(), task.Handle)
		if err != nil {
			t.Fatalf("GetTask(%d) error = %v", index, err)
		}
		want := domain.TaskCandidateComplete
		if index == len(publications)-1 {
			want = domain.TaskDelivered
		}
		if updated.State != want {
			t.Fatalf("task state after evidence %d = %q, want %q", index, updated.State, want)
		}
	}
	var candidateReports int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM reports
		WHERE task_handle = ? AND kind = 'candidate_complete'`, task.Handle).Scan(&candidateReports); err != nil {
		t.Fatal(err)
	}
	if candidateReports != 0 {
		t.Fatalf("candidate reports = %d, want no synthetic worker report", candidateReports)
	}
}

func insertEvidenceReconciliation(t *testing.T, store *Store, task domain.Task, headRevision string) {
	t.Helper()
	workspace := "/approved/worktrees/" + task.Handle
	if _, err := store.db.Exec(`INSERT INTO task_preparations(
		task_handle, external_run_ref, registration_nonce, expires_at, created_at,
		requested_workspace_root, state, requested_attachment_kind, requested_attachment_source_path)
		VALUES (?, ?, 'registration-recovery-0001', ?, ?, ?, 'open', 'unix_socket', ?)`,
		task.Handle, task.Handle, formatTime(task.CreatedAt.Add(time.Hour)), formatTime(task.CreatedAt),
		workspace, "/approved/runtime/"+task.Handle+"/attachment.sock"); err != nil {
		t.Fatalf("insert task preparation: %v", err)
	}
	preparation := storeOperation("prepare-recovery-delivery", 1)
	preparation.ResultRef = task.Handle
	if err := store.RecordOperation(context.Background(), preparation); err != nil {
		t.Fatalf("RecordOperation(preparation) error = %v", err)
	}
	if _, err := store.db.Exec("UPDATE tasks SET state_version = 2 WHERE handle = ?", task.Handle); err != nil {
		t.Fatalf("advance fixture task version: %v", err)
	}
	reconciliation := storeOperation("reconcile-recovery-delivery", 2)
	reconciliation.Command = "ReconcileTask"
	reconciliation.ResultRef = task.Handle
	if err := store.RecordOperation(context.Background(), reconciliation); err != nil {
		t.Fatalf("RecordOperation(reconciliation) error = %v", err)
	}
	terminalAt := task.UpdatedAt
	if _, err := store.db.Exec(`INSERT INTO task_terminal_bindings(
		task_handle, managed_run_id, workspace_lease_id, terminal_session_id,
		latest_transition, running_observed, updated_at)
		VALUES (?, ?, ?, 'terminal-recovery-delivery', 'exited', 1, ?)`,
		task.Handle, task.ManagedRunID, task.WorkspaceLeaseID, formatTime(terminalAt)); err != nil {
		t.Fatalf("insert terminal binding: %v", err)
	}
	if _, err := store.db.Exec(`INSERT INTO task_candidate_reconciliations(
		operation_id, task_handle, action, preparation_operation_id,
		repository_id, worktree_path, branch, base_revision, head_revision,
		cleanliness, terminal_session_id, terminal_transition,
		terminal_observed_at, observed_at, started_state_version, completed_state_version)
			VALUES (?, ?, 'validate-clean-candidate', ?, ?, ?, ?, ?, ?, 'clean',
			'terminal-recovery-delivery', 'exited', ?, ?, 1, 2)`,
		reconciliation.ID, task.Handle, preparation.ID, task.RepositoryID, workspace,
		"devcrew/"+task.Handle+"-reconciled", task.BaseRevision, headRevision,
		formatTime(terminalAt), formatTime(terminalAt)); err != nil {
		t.Fatalf("insert reconciliation evidence: %v", err)
	}
}

func TestCandidateEvidenceStore_RejectsAlteredPublicationReplay(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	task := candidateEvidenceTask(t, "task-evidence-replay")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	publications := candidateEvidencePublications(t, task, sealed)
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	publications[1].Body = []byte("https://example.com/pull/18")
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitCandidateEvidence(altered publication) error = %v, want ErrConflict", err)
	}
}

func TestComisEvidenceOutbox_PersistsReportArtifactDelivery(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := candidateEvidenceTask(t, "task-report-artifact")
	task.Shape = domain.ShapeScout
	task.DeliveryMode = domain.DeliveryReport
	task, err = task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	if err := task.Validate(); err != nil {
		t.Fatalf("report task is invalid: %v", err)
	}
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	body := []byte("bounded scout report")
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	producedAt := task.UpdatedAt.Add(4 * time.Minute)
	head := strings.Repeat("b", 40)
	sealed, err := domain.SealDeliveryEvidence(domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: head, WorktreeCleanliness: domain.WorktreeClean,
		ValidationReceipts: []domain.ValidationEvidenceReceipt{{
			CheckID: "unit", ProgramID: "go-test", HeadRevision: head,
			Conclusion: domain.CheckPassed, Required: true, OutputHash: strings.Repeat("d", 64),
			StartedAt: producedAt.Add(-time.Minute), CompletedAt: producedAt,
		}},
		ReportArtifact: &domain.ReportArtifactEvidence{
			ContentHash: contentHash, Size: int64(len(body)), MediaType: "text/plain",
		},
		ProducedAt: producedAt, ExpiresAt: producedAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SealDeliveryEvidence() error = %v", err)
	}
	expiresAt := sealed.Bundle().ExpiresAt
	publications := []application.ComisEvidencePublication{
		{
			OperationID: "put-report-bundle", TaskHandle: task.Handle,
			EvidenceRef: "report-bundle", Kind: "candidate_bundle", SubjectDigest: sealed.Digest(),
			ObservedAt: producedAt, ExpiresAt: &expiresAt, ContentHash: sealed.Digest(),
			VerificationLevel: application.ComisEvidenceAdapterVerified, Body: sealed.Canonical(),
		},
		{
			OperationID: "put-report-artifact", TaskHandle: task.Handle,
			EvidenceRef: "report-artifact", Kind: "report_artifact", SubjectDigest: sealed.Digest(),
			ObservedAt: producedAt, ExpiresAt: &expiresAt, ContentHash: contentHash,
			VerificationLevel: application.ComisEvidenceAdapterVerified, Body: body,
			Delivery: &application.ComisEvidenceDeliveryRequest{
				Kind: application.ComisEvidenceAttachment, FileName: "report.txt", MediaType: "text/plain",
			},
		},
	}
	if _, judgment, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, nil, producedAt.Add(time.Minute), publications,
	); err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("CommitCandidateEvidence(report) = %#v, %v", judgment, err)
	}
	first, found, err := store.NextComisEvidence(context.Background())
	if err != nil || !found || first.Delivery == nil || first.Delivery.Kind != application.ComisEvidenceAttachment ||
		first.Delivery.FileName != "report.txt" || first.Delivery.MediaType != "text/plain" {
		t.Fatalf("NextComisEvidence(attachment) = %#v, %t, %v", first, found, err)
	}
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, application.ComisEvidenceAcknowledgement{
		ManagedRunID: first.ManagedRunID, EvidenceRef: first.EvidenceRef,
		ContentHash: first.ContentHash, VerificationLevel: first.VerificationLevel,
	}, producedAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkComisEvidenceDelivered(bundle) error = %v", err)
	}
	second, found, err := store.NextComisEvidence(context.Background())
	if err != nil || !found || second.Delivery != nil {
		t.Fatalf("NextComisEvidence(bundle) = %#v, %t, %v", second, found, err)
	}
}

func TestCandidatePublicationValidation_RejectsMalformedAndDuplicatedItems(t *testing.T) {
	task := candidateEvidenceTask(t, "task-publication-validation")
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	publications := candidateEvidencePublications(t, task, sealed)
	if err := validateCandidatePublications(task, nil, publications); err == nil {
		t.Fatal("validateCandidatePublications(nil evidence) error = nil")
	}
	invalid := append([]application.ComisEvidencePublication(nil), publications...)
	invalid[0].TaskHandle = "task-other"
	if err := validateCandidatePublications(task, sealed, invalid); err == nil {
		t.Fatal("validateCandidatePublications(invalid publication) error = nil")
	}
	duplicateOperation := append([]application.ComisEvidencePublication(nil), publications...)
	duplicateOperation[1].OperationID = duplicateOperation[0].OperationID
	if err := validateCandidatePublications(task, sealed, duplicateOperation); err == nil {
		t.Fatal("validateCandidatePublications(duplicate operation) error = nil")
	}
	duplicateReference := append([]application.ComisEvidencePublication(nil), publications...)
	duplicateReference[1].EvidenceRef = duplicateReference[0].EvidenceRef
	if err := validateCandidatePublications(task, sealed, duplicateReference); err == nil {
		t.Fatal("validateCandidatePublications(duplicate reference) error = nil")
	}
	wrongOutcome := append([]application.ComisEvidencePublication(nil), publications...)
	wrongOutcome[0].Kind = "delivery_reference"
	if err := validateCandidatePublications(task, sealed, wrongOutcome); err == nil {
		t.Fatal("validateCandidatePublications(wrong outcome) error = nil")
	}
	badReference := append([]application.ComisEvidencePublication(nil), publications...)
	badReference[1].Body = []byte("https://user@example.com/pull/17")
	badReference[1].ContentHash = fmt.Sprintf("%x", sha256.Sum256(badReference[1].Body))
	if err := validateCandidatePublications(task, sealed, badReference); err == nil {
		t.Fatal("validateCandidatePublications(bad reference) error = nil")
	}
}

func TestComisEvidenceOutbox_RejectsInvalidAcknowledgementsAndCorruptRows(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := candidateEvidenceTask(t, "task-evidence-errors")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	publications := candidateEvidencePublications(t, task, sealed)
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	if err := store.MarkComisEvidenceDelivered(context.Background(), "", application.ComisEvidenceAcknowledgement{}, time.Time{}); err == nil {
		t.Fatal("MarkComisEvidenceDelivered(invalid) error = nil")
	}
	first, found, err := store.NextComisEvidence(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisEvidence() = %#v, %t, %v", first, found, err)
	}
	badRetention := judgedAt
	ack := application.ComisEvidenceAcknowledgement{
		ManagedRunID: first.ManagedRunID, EvidenceRef: first.EvidenceRef,
		ContentHash: first.ContentHash, VerificationLevel: first.VerificationLevel,
		RetainedUntil: &badRetention,
	}
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, ack, judgedAt); err == nil {
		t.Fatal("MarkComisEvidenceDelivered(bad retention) error = nil")
	}
	ack.RetainedUntil = nil
	if err := store.MarkComisEvidenceDelivered(context.Background(), "evidence-unknown", ack, judgedAt); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("MarkComisEvidenceDelivered(unknown) error = %v, want ErrNotFound", err)
	}
	altered := ack
	altered.EvidenceRef = "evidence-altered"
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, altered, judgedAt); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("MarkComisEvidenceDelivered(altered) error = %v, want ErrConflict", err)
	}
	retainedUntil := judgedAt.Add(time.Hour)
	ack.RetainedUntil = &retainedUntil
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, ack, judgedAt); err != nil {
		t.Fatalf("MarkComisEvidenceDelivered() error = %v", err)
	}
	ack.RetainedUntil = nil
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, ack, judgedAt); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("MarkComisEvidenceDelivered(altered replay) error = %v, want ErrConflict", err)
	}
	if _, err := store.db.Exec("UPDATE comis_evidence_outbox SET observed_at = 'not-a-time' WHERE operation_id = ?", publications[1].OperationID); err != nil {
		t.Fatalf("corrupt evidence row: %v", err)
	}
	if _, _, err := store.NextComisEvidence(context.Background()); err == nil {
		t.Fatal("NextComisEvidence(corrupt observation time) error = nil")
	}
	if _, err := store.db.Exec("UPDATE comis_evidence_outbox SET observed_at = ?, expires_at = 'not-a-time' WHERE operation_id = ?",
		formatTime(publications[1].ObservedAt), publications[1].OperationID); err != nil {
		t.Fatalf("corrupt evidence expiry: %v", err)
	}
	if _, _, err := store.NextComisEvidence(context.Background()); err == nil {
		t.Fatal("NextComisEvidence(corrupt expiry time) error = nil")
	}
	if _, err := store.db.Exec("UPDATE comis_evidence_outbox SET expires_at = NULL, content_hash = ? WHERE operation_id = ?",
		strings.Repeat("0", 64), publications[1].OperationID); err != nil {
		t.Fatalf("corrupt evidence hash: %v", err)
	}
	if _, _, err := store.NextComisEvidence(context.Background()); err == nil {
		t.Fatal("NextComisEvidence(corrupt durable publication) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NextComisEvidence(context.Background()); err == nil {
		t.Fatal("NextComisEvidence(closed) error = nil")
	}
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, ack, judgedAt); err == nil {
		t.Fatal("MarkComisEvidenceDelivered(closed) error = nil")
	}
}

func TestComisEvidenceOutbox_FailsClosedAcrossTransactionalBoundaries(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := candidateEvidenceTask(t, "task-evidence-transaction")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	publications := candidateEvidencePublications(t, task, sealed)
	if _, found, err := store.NextComisEvidence(context.Background()); err != nil || found {
		t.Fatalf("NextComisEvidence(empty) found = %t, error = %v", found, err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := insertCandidatePublications(context.Background(), transaction, task, nil, publications, 2); err == nil {
		t.Fatal("insertCandidatePublications(invalid) error = nil")
	}
	if matches, err := candidatePublicationsMatch(context.Background(), transaction, publications); err != nil || matches {
		t.Fatalf("candidatePublicationsMatch(missing) = %t, %v", matches, err)
	}
	if err := insertCandidatePublications(context.Background(), transaction, task, sealed, publications, 2); err != nil {
		t.Fatalf("insertCandidatePublications() error = %v", err)
	}
	if err := insertCandidatePublications(context.Background(), transaction, task, sealed, publications, 2); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("insertCandidatePublications(duplicate) error = %v, want ErrConflict", err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if err := insertCandidatePublications(context.Background(), transaction, task, sealed, publications, 2); err == nil {
		t.Fatal("insertCandidatePublications(closed transaction) error = nil")
	}
	if _, err := candidatePublicationsMatch(context.Background(), transaction, publications); err == nil {
		t.Fatal("candidatePublicationsMatch(closed transaction) error = nil")
	}
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	first, found, err := store.NextComisEvidence(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisEvidence() = %#v, %t, %v", first, found, err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_evidence_ack_update BEFORE UPDATE ON comis_evidence_outbox
            BEGIN SELECT RAISE(FAIL, 'evidence acknowledgement unavailable'); END`); err != nil {
		t.Fatalf("create acknowledgement trigger: %v", err)
	}
	if err := store.MarkComisEvidenceDelivered(context.Background(), first.OperationID, application.ComisEvidenceAcknowledgement{
		ManagedRunID: first.ManagedRunID, EvidenceRef: first.EvidenceRef,
		ContentHash: first.ContentHash, VerificationLevel: first.VerificationLevel,
	}, judgedAt.Add(time.Minute)); err == nil {
		t.Fatal("MarkComisEvidenceDelivered(write failure) error = nil")
	}
	if _, err := store.db.Exec("ALTER TABLE candidate_evidence RENAME TO unavailable_candidate_records"); err != nil {
		t.Fatalf("rename candidate evidence table: %v", err)
	}
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err == nil {
		t.Fatal("CommitCandidateEvidence(unavailable records) error = nil")
	}
}
