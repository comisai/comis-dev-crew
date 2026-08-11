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

type candidateEvidenceStore interface {
	CommitCandidateEvidence(context.Context, string, *domain.SealedDeliveryEvidence, []string, []string, time.Time) (domain.Task, domain.CandidateJudgment, error)
	LatestCandidateEvidence(context.Context, string) (*domain.SealedDeliveryEvidence, domain.CandidateJudgment, error)
}

func TestCandidateEvidenceStore_PersistsExactJudgmentAndAdvancesAcceptedTaskOnce(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	evidenceStore, ok := any(store).(candidateEvidenceStore)
	if !ok {
		t.Fatal("Store does not implement durable candidate evidence")
	}
	task := candidateEvidenceTask(t, "task-evidence")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	updated, judgment, err := evidenceStore.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt,
	)
	if err != nil || judgment.Outcome != domain.CandidateAccepted || updated.State != domain.TaskCandidateComplete {
		t.Fatalf("CommitCandidateEvidence() = %#v, %#v, %v", updated, judgment, err)
	}
	if updated.StateVersion != task.StateVersion+1 {
		t.Fatalf("candidate state version = %d, want %d", updated.StateVersion, task.StateVersion+1)
	}
	replayed, replayJudgment, err := evidenceStore.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt,
	)
	if err != nil || !reflect.DeepEqual(replayed, updated) || replayJudgment != judgment {
		t.Fatalf("CommitCandidateEvidence(replay) = %#v, %#v, %v", replayed, replayJudgment, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	defer func() { _ = reopened.Close() }()
	reopenedEvidenceStore, ok := any(reopened).(candidateEvidenceStore)
	if !ok {
		t.Fatal("reopened Store does not implement durable candidate evidence")
	}
	stored, storedJudgment, err := reopenedEvidenceStore.LatestCandidateEvidence(context.Background(), task.Handle)
	if err != nil || stored.Digest() != sealed.Digest() || storedJudgment != judgment {
		t.Fatalf("LatestCandidateEvidence() = %#v, %#v, %v", stored, storedJudgment, err)
	}
	if string(stored.Canonical()) != string(sealed.Canonical()) {
		t.Fatal("stored evidence canonical bytes differ")
	}
}

func TestCandidateEvidenceStore_RefusesCrossTaskStaleAndCorruptEvidence(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	evidenceStore, ok := any(store).(candidateEvidenceStore)
	if !ok {
		t.Fatal("Store does not implement durable candidate evidence")
	}
	task := candidateEvidenceTask(t, "task-evidence")
	other := candidateEvidenceTask(t, "task-other")
	for _, candidate := range []domain.Task{task, other} {
		if err := store.CreateTask(context.Background(), candidate); err != nil {
			t.Fatalf("CreateTask(%q) error = %v", candidate.Handle, err)
		}
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	if _, _, err := evidenceStore.CommitCandidateEvidence(
		context.Background(), other.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, task.UpdatedAt.Add(5*time.Minute),
	); err == nil {
		t.Fatal("CommitCandidateEvidence(cross task) error = nil")
	}
	unknown, judgment, err := evidenceStore.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"missing"}, []string{"ci/unit"}, task.UpdatedAt.Add(5*time.Minute),
	)
	if err != nil || judgment.Outcome != domain.CandidateUnknown || unknown.State != domain.TaskValidating {
		t.Fatalf("CommitCandidateEvidence(missing check) = %#v, %#v, %v", unknown, judgment, err)
	}
	if _, _, err := evidenceStore.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, task.UpdatedAt.Add(20*time.Minute),
	); err == nil {
		t.Fatal("CommitCandidateEvidence(altered replay) error = nil")
	}
	if _, err := store.db.Exec(`UPDATE candidate_evidence SET canonical = '{}' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatalf("corrupt candidate evidence: %v", err)
	}
	if _, _, err := evidenceStore.LatestCandidateEvidence(context.Background(), task.Handle); err == nil {
		t.Fatal("LatestCandidateEvidence(corrupt) error = nil")
	}
	if _, _, err := evidenceStore.LatestCandidateEvidence(context.Background(), "task-missing"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("LatestCandidateEvidence(missing) error = %v, want ErrNotFound", err)
	}
}

func candidateEvidenceTask(t *testing.T, handle string) domain.Task {
	t.Helper()
	task := storeTask(handle, 1)
	task.State = domain.TaskValidating
	task.ManagedRunID = "managed-run-evidence"
	task.WorkspaceLeaseID = "workspace-lease-evidence"
	if err := task.Validate(); err != nil {
		t.Fatalf("candidate evidence task is invalid: %v", err)
	}
	return task
}

func candidateEvidence(t *testing.T, task domain.Task, head string) *domain.SealedDeliveryEvidence {
	t.Helper()
	producedAt := task.UpdatedAt.Add(4 * time.Minute)
	sealed, err := domain.SealDeliveryEvidence(domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: head, WorktreeCleanliness: domain.WorktreeClean,
		ValidationReceipts: []domain.ValidationEvidenceReceipt{{
			CheckID: "unit", ProgramID: "go-test", HeadRevision: head,
			Conclusion: domain.CheckPassed, Required: true, OutputHash: strings.Repeat("d", 64),
			StartedAt: producedAt.Add(-time.Minute), CompletedAt: producedAt,
		}},
		ForgeEvidence: &domain.ForgeEvidence{
			Repository: task.RepositoryID, PullRequestID: "pull-request-evidence", HeadRevision: head,
			CheckConclusions: []domain.ForgeCheckEvidence{{Name: "ci/unit", Conclusion: domain.CheckPassed}},
		},
		ProducedAt: producedAt, ExpiresAt: producedAt.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("SealDeliveryEvidence() error = %v", err)
	}
	return sealed
}
