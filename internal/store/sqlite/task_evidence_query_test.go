package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestStore_ReadTaskEvidenceProjectsContentFreeDurableReferences(t *testing.T) {
	store, task, sealed := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "evidence.db"))
	if _, err := store.db.Exec(`INSERT INTO task_cleanup_holds(
		task_handle, hold_id, reason, opened_at) VALUES (?, ?, ?, ?)`,
		task.Handle, "cleanup-hold-0001", "operator review", formatTime(time.Now().UTC())); err != nil {
		t.Fatalf("insert cleanup hold: %v", err)
	}

	evidence, err := store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ReadTaskEvidence() error = %v", err)
	}
	if evidence.Candidate.Status != "judged" || evidence.Candidate.HeadRevision != sealed.Bundle().HeadRevision ||
		evidence.Candidate.EvidenceDigest != sealed.Digest() {
		t.Fatalf("candidate evidence = %#v, want exact sealed head and digest", evidence.Candidate)
	}
	if evidence.Activity.Status != "authenticated_report" || evidence.Activity.ReportID != "report-cleanup-candidate" ||
		evidence.Activity.ReportKind != "candidate_complete" || evidence.Activity.AcceptedAtMs == 0 {
		t.Fatalf("report activity = %#v, want latest accepted report", evidence.Activity)
	}
	if evidence.Decision.Status != "none" || evidence.Validation.Status != "accepted" {
		t.Fatalf("decision/validation evidence = %#v / %#v", evidence.Decision, evidence.Validation)
	}
	if evidence.Delivery.Status != "delivered" ||
		evidence.Delivery.PullRequestID != sealed.Bundle().ForgeEvidence.PullRequestID ||
		evidence.Delivery.EvidenceOperationID == "" || evidence.Delivery.EvidenceRef == "" {
		t.Fatalf("delivery evidence = %#v, want delivered forge and outbox references", evidence.Delivery)
	}
	if evidence.Cleanup.Status != "held" || evidence.Cleanup.OpenHoldCount != 1 {
		t.Fatalf("cleanup evidence = %#v, want one open hold", evidence.Cleanup)
	}
	if evidence.Authority.ManagedRunID != task.ManagedRunID ||
		evidence.Authority.WorkspaceLeaseID != task.WorkspaceLeaseID ||
		evidence.Authority.ExecutionAttachmentID != task.ExecutionAttachmentID ||
		evidence.Authority.PreparationOperationID != "prepare-cleanup-0001" {
		t.Fatalf("authority references = %#v, want exact durable identities", evidence.Authority)
	}
}
