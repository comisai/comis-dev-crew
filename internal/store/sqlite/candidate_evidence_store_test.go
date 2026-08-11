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

func TestCandidateEvidenceStore_FailsClosedForInvalidInputsStateAndVerdicts(t *testing.T) {
	now := time.Date(2026, time.August, 11, 18, 0, 0, 0, time.UTC)
	invalidJudgments := []domain.CandidateJudgment{
		{},
		{Outcome: domain.CandidateAccepted, Reason: domain.CandidateEvidenceInvalid},
		{Outcome: domain.CandidateRejected, Reason: domain.CandidateEvidenceAccepted},
		{Outcome: domain.CandidateUnknown, Reason: domain.CandidateEvidenceAccepted},
		{Outcome: "forged", Reason: domain.CandidateEvidenceInvalid},
	}
	for _, judgment := range invalidJudgments {
		if validCandidateJudgment(judgment) {
			t.Fatalf("validCandidateJudgment(%#v) = true", judgment)
		}
	}
	validJudgments := []domain.CandidateJudgment{
		{Outcome: domain.CandidateAccepted, Reason: domain.CandidateEvidenceAccepted},
		{Outcome: domain.CandidateRejected, Reason: domain.CandidateValidationFailed},
		{Outcome: domain.CandidateRejected, Reason: domain.CandidateForgeFailed},
	}
	for _, reason := range []domain.CandidateReason{
		domain.CandidateEvidenceInvalid, domain.CandidateEvidenceStale, domain.CandidateEvidenceConflicting,
		domain.CandidateWorktreeUnverified, domain.CandidateDecisionUnresolved, domain.CandidateValidationMissing,
		domain.CandidateValidationUnknown, domain.CandidateForgeMissing, domain.CandidateForgeUnknown,
		domain.CandidateReportMissing,
	} {
		validJudgments = append(validJudgments, domain.CandidateJudgment{Outcome: domain.CandidateUnknown, Reason: reason})
	}
	for _, judgment := range validJudgments {
		if !validCandidateJudgment(judgment) {
			t.Fatalf("validCandidateJudgment(%#v) = false", judgment)
		}
	}

	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	prepared := storeTask("task-prepared-evidence", 1)
	if err := store.CreateTask(context.Background(), prepared); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, prepared, strings.Repeat("b", 40))
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), prepared.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, now,
	); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitCandidateEvidence(prepared) error = %v, want ErrPrecondition", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.CommitCandidateEvidence(cancelled, prepared.Handle, sealed, nil, nil, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitCandidateEvidence(cancelled) error = %v", err)
	}
	if _, _, err := store.LatestCandidateEvidence(cancelled, prepared.Handle); !errors.Is(err, context.Canceled) {
		t.Fatalf("LatestCandidateEvidence(cancelled) error = %v", err)
	}
	//lint:ignore SA1012 The boundary test proves nil contexts are rejected before database work.
	if _, _, err := store.CommitCandidateEvidence(nil, prepared.Handle, sealed, nil, nil, now); err == nil {
		t.Fatal("CommitCandidateEvidence(nil context) error = nil")
	}
	if _, _, err := store.CommitCandidateEvidence(context.Background(), "bad handle", sealed, nil, nil, now); err == nil {
		t.Fatal("CommitCandidateEvidence(invalid handle) error = nil")
	}
	if _, _, err := store.CommitCandidateEvidence(context.Background(), prepared.Handle, nil, nil, nil, now); err == nil {
		t.Fatal("CommitCandidateEvidence(nil evidence) error = nil")
	}
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), prepared.Handle, sealed, nil, nil, now.In(time.FixedZone("other", 0)),
	); err == nil {
		t.Fatal("CommitCandidateEvidence(non-UTC time) error = nil")
	}
	//lint:ignore SA1012 The boundary test proves nil contexts are rejected before database work.
	if _, _, err := store.LatestCandidateEvidence(nil, prepared.Handle); err == nil {
		t.Fatal("LatestCandidateEvidence(nil context) error = nil")
	}
	if _, _, err := store.LatestCandidateEvidence(context.Background(), "bad handle"); err == nil {
		t.Fatal("LatestCandidateEvidence(invalid handle) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, err := store.CommitCandidateEvidence(context.Background(), prepared.Handle, sealed, nil, nil, now); err == nil {
		t.Fatal("CommitCandidateEvidence(closed store) error = nil")
	}
	if _, _, err := store.LatestCandidateEvidence(context.Background(), prepared.Handle); err == nil {
		t.Fatal("LatestCandidateEvidence(closed store) error = nil")
	}
	if _, _, err := (*Store)(nil).CommitCandidateEvidence(context.Background(), prepared.Handle, sealed, nil, nil, now); err == nil {
		t.Fatal("CommitCandidateEvidence(nil store) error = nil")
	}
	if _, _, err := (*Store)(nil).LatestCandidateEvidence(context.Background(), prepared.Handle); err == nil {
		t.Fatal("LatestCandidateEvidence(nil store) error = nil")
	}
}

func TestCandidateEvidenceStore_RollsBackStorageFailuresAndRejectsCorruptMetadata(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	task := candidateEvidenceTask(t, "task-evidence-storage")
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, task, strings.Repeat("b", 40))
	judgedAt := task.UpdatedAt.Add(5 * time.Minute)
	if _, err := store.db.Exec(`CREATE TRIGGER reject_candidate_task_update BEFORE UPDATE ON tasks BEGIN SELECT RAISE(FAIL, 'blocked'); END`); err != nil {
		t.Fatalf("create task update trigger: %v", err)
	}
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt,
	); err == nil {
		t.Fatal("CommitCandidateEvidence(blocked task update) error = nil")
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_candidate_task_update`); err != nil {
		t.Fatalf("drop task update trigger: %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER reject_candidate_evidence_insert BEFORE INSERT ON candidate_evidence BEGIN SELECT RAISE(FAIL, 'blocked'); END`); err != nil {
		t.Fatalf("create evidence insert trigger: %v", err)
	}
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt,
	); err == nil {
		t.Fatal("CommitCandidateEvidence(blocked evidence insert) error = nil")
	}
	if _, err := store.db.Exec(`DROP TRIGGER reject_candidate_evidence_insert`); err != nil {
		t.Fatalf("drop evidence insert trigger: %v", err)
	}
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), task.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt,
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	transaction, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	row, found, err := findCandidateEvidence(context.Background(), transaction, task.Handle, sealed.Digest())
	if err != nil || !found {
		t.Fatalf("findCandidateEvidence() = %#v, %v, %v", row, found, err)
	}
	if err := insertCandidateEvidence(context.Background(), transaction, row); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("insertCandidateEvidence(duplicate) error = %v, want ErrConflict", err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE candidate_evidence SET outcome = 'forged' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatalf("corrupt candidate outcome: %v", err)
	}
	if _, _, err := store.LatestCandidateEvidence(context.Background(), task.Handle); err == nil {
		t.Fatal("LatestCandidateEvidence(corrupt outcome) error = nil")
	}
	if _, err := store.db.Exec(`UPDATE candidate_evidence SET outcome = 'accepted', reason = 'evidence_accepted', judged_at = 'invalid' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatalf("corrupt candidate time: %v", err)
	}
	if _, _, err := store.LatestCandidateEvidence(context.Background(), task.Handle); err == nil {
		t.Fatal("LatestCandidateEvidence(corrupt time) error = nil")
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
