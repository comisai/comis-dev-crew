package sqlite

import (
	"context"
	"errors"
	"path/filepath"
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
