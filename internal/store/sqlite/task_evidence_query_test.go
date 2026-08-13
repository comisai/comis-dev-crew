package sqlite

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
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

func TestStore_ReadTaskEvidenceDoesNotOverclaimPendingDelivery(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "pending.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := candidateEvidenceTask(t, "task-pending-delivery")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	publications := candidateEvidencePublications(t, task, sealed)
	if _, judgment, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"},
		sealed.Bundle().ProducedAt, publications,
	); err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("CommitCandidateEvidence() = %#v, %v", judgment, err)
	}
	for index := range publications {
		delivery, found, err := store.NextComisEvidence(context.Background())
		if err != nil || !found {
			t.Fatalf("NextComisEvidence(%d) = %#v, %t, %v", index, delivery, found, err)
		}
		deliveredAt := sealed.Bundle().ProducedAt.Add(time.Duration(index+1) * time.Minute)
		retainedUntil := deliveredAt.Add(time.Hour)
		if err := store.MarkComisEvidenceDelivered(context.Background(), delivery.OperationID, application.ComisEvidenceAcknowledgement{
			ManagedRunID: delivery.ManagedRunID, EvidenceRef: delivery.EvidenceRef,
			ContentHash: delivery.ContentHash, VerificationLevel: delivery.VerificationLevel,
			RetainedUntil: &retainedUntil,
		}, deliveredAt); err != nil {
			t.Fatalf("MarkComisEvidenceDelivered(%d) error = %v", index, err)
		}
	}
	evidence, err := store.ReadTaskEvidence(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ReadTaskEvidence() error = %v", err)
	}
	if evidence.Delivery.Status != application.DeliveryEvidencePending {
		t.Fatalf("delivery status = %q, want pending until final task delivery", evidence.Delivery.Status)
	}
}

func TestStore_TaskEvidenceSnapshotsShareOneDurableReadBoundary(t *testing.T) {
	store, task, sealed := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "snapshot.db"))
	observation, err := store.ReadTaskObservation(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("ReadTaskObservation() error = %v", err)
	}
	if observation.Task.Handle != task.Handle || observation.Task.StateVersion != task.StateVersion ||
		observation.Evidence.Candidate.EvidenceDigest != sealed.Digest() {
		t.Fatalf("task observation = %#v, want one exact task/evidence snapshot", observation)
	}
	observations, stateVersion, err := store.TaskEvidenceSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TaskEvidenceSnapshot() error = %v", err)
	}
	if len(observations) != 1 || !reflect.DeepEqual(observations[0], observation) || stateVersion != task.StateVersion {
		t.Fatalf("task evidence snapshot = %#v version %d, want observation %#v version %d",
			observations, stateVersion, observation, task.StateVersion)
	}
}
