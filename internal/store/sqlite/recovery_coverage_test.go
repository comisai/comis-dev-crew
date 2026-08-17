package sqlite

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type recoveryFailingExecer struct{ err error }

func (failure recoveryFailingExecer) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, failure.err
}

func TestRecoveryStoresRejectCanceledAndClosedAuthority(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 1, 0, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-recovery-coverage", TaskHandle: "task-recovery-coverage",
		SubjectDigest: strings.Repeat("a", 64), CreatedAt: now,
	}
	seed := [32]byte{1}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle:    intent.TaskHandle,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		RelaySeed:     seed,
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), application.TaskPreparationIntent{}); err == nil {
		t.Fatal("invalid preparation intent was recorded")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RecordTaskPreparationIntent(canceled, intent); !errors.Is(err, context.Canceled) {
		t.Fatalf("RecordTaskPreparationIntent(canceled) error = %v", err)
	}
	if _, err := store.ListTaskPreparationIntents(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListTaskPreparationIntents(canceled) error = %v", err)
	}
	if _, err := store.ListRuntimeRelayIdentityUpgrades(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades(canceled) error = %v", err)
	}
	if _, err := store.ListRuntimeRelayIdentityRefusals(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRuntimeRelayIdentityRefusals(canceled) error = %v", err)
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(canceled, upgrade); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteRuntimeRelayIdentityUpgrade(canceled) error = %v", err)
	}
	if err := store.RefuseRuntimeRelayIdentityUpgrade(canceled, upgrade, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefuseRuntimeRelayIdentityUpgrade(canceled) error = %v", err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(canceled, intent.TaskHandle, now); !errors.Is(err, context.Canceled) {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(canceled) error = %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), intent); err == nil {
		t.Fatal("closed store recorded a preparation intent")
	}
	if _, err := store.ListTaskPreparationIntents(context.Background()); err == nil {
		t.Fatal("closed store listed preparation intents")
	}
	if _, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err == nil {
		t.Fatal("closed store listed relay upgrades")
	}
	if _, err := store.ListRuntimeRelayIdentityRefusals(context.Background()); err == nil {
		t.Fatal("closed store listed relay refusals")
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(context.Background(), upgrade); err == nil {
		t.Fatal("closed store completed a relay upgrade")
	}
	if err := store.RefuseRuntimeRelayIdentityUpgrade(context.Background(), upgrade, now); err == nil {
		t.Fatal("closed store refused a relay upgrade")
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), intent.TaskHandle, now); err == nil {
		t.Fatal("closed store refused task recovery")
	}
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, now); err == nil {
		t.Fatal("closed store refused preparation recovery")
	}
	if _, _, err := store.GetTaskCleanupRecord(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read a cleanup record")
	}
	if _, err := readTaskPreparationIntent(context.Background(), store.db, intent.OperationID); err == nil {
		t.Fatal("closed store read a preparation intent")
	}
	if err := store.applyTaskPreparationMigrations(context.Background()); err == nil {
		t.Fatal("closed store applied task preparation migrations")
	}
	if err := store.applyRuntimeRelayUpgradeMigration(context.Background()); err == nil {
		t.Fatal("closed store applied the relay upgrade migration")
	}
	if err := store.applyComisReportOutboxMigration(context.Background()); err == nil {
		t.Fatal("closed store applied the report outbox migration")
	}
	if err := store.migrate(context.Background()); err == nil {
		t.Fatal("closed store applied migrations")
	}
	if _, _, err := store.LatestCandidateEvidence(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read candidate evidence")
	}
	if _, _, err := store.NextComisEvidence(context.Background()); err == nil {
		t.Fatal("closed store read the evidence outbox")
	}
	if _, _, err := store.NextComisReport(context.Background()); err == nil {
		t.Fatal("closed store read the report outbox")
	}
	if _, err := store.deliveredCandidateEvidenceRefs(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read delivered evidence references")
	}
	if _, err := store.ReadTaskEvidence(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read task evidence")
	}
	if _, _, err := store.TaskEvidenceSnapshot(context.Background()); err == nil {
		t.Fatal("closed store read the task evidence snapshot")
	}
	if _, err := store.ReadTaskRecoveryEvidence(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read task recovery evidence")
	}
	if _, err := store.ReadTaskReconciliationAuthority(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read task reconciliation authority")
	}
	if _, _, err := store.ReadReconciledCandidateSnapshot(context.Background(), intent.TaskHandle); err == nil {
		t.Fatal("closed store read a reconciled candidate snapshot")
	}
	if _, err := store.ListActiveValidationProcesses(context.Background()); err == nil {
		t.Fatal("closed store listed validation processes")
	}
	if _, _, err := findValidationProcess(context.Background(), store.db, "operation-closed-store"); err == nil {
		t.Fatal("closed store found a validation process")
	}
	if _, _, err := findCandidateEvidence(context.Background(), store.db, intent.TaskHandle, intent.SubjectDigest); err == nil {
		t.Fatal("closed store found candidate evidence")
	}
	if _, err := taskPreparationOperationID(context.Background(), store.db, intent.TaskHandle); err == nil {
		t.Fatal("closed store read the preparation operation")
	}
	if _, _, err := findTerminalBinding(context.Background(), store.db, intent.TaskHandle); err == nil {
		t.Fatal("closed store found a terminal binding")
	}
	if _, err := getTaskByBinding(context.Background(), store.db, "managed-run-closed", "workspace-lease-closed"); err == nil {
		t.Fatal("closed store found a task by binding")
	}
	if _, _, _, err := lastSettledTerminalPosture(
		context.Background(), store.db, intent.TaskHandle, "terminal-session-closed",
	); err == nil {
		t.Fatal("closed store read the terminal posture")
	}
	if _, err := candidateRecoveryHistoryExists(context.Background(), store.db, intent.TaskHandle); err == nil {
		t.Fatal("closed store read candidate recovery history")
	}
	closedTask := storeTask(intent.TaskHandle, 1)
	evidence := application.TaskEvidenceView{
		Validation: application.ValidationEvidenceView{Status: application.ValidationEvidenceNotStarted},
	}
	if err := readCandidateDiagnostic(context.Background(), store.db, closedTask, &evidence); err == nil {
		t.Fatal("closed store read the candidate diagnostic")
	}
	if err := readReconciliationCandidateOrigin(
		context.Background(), store.db, closedTask.Handle, "", &evidence.Candidate,
	); err == nil {
		t.Fatal("closed store read the reconciliation diagnostic")
	}
	if err := readReportDiagnostic(context.Background(), store.db, closedTask.Handle, &evidence); err == nil {
		t.Fatal("closed store read the report diagnostic")
	}
	if err := readDecisionDiagnostic(context.Background(), store.db, closedTask.Handle, &evidence); err == nil {
		t.Fatal("closed store read the decision diagnostic")
	}
	if err := readValidationDiagnostic(context.Background(), store.db, closedTask, &evidence); err == nil {
		t.Fatal("closed store read the validation diagnostic")
	}
	if err := readDeliveryDiagnostic(context.Background(), store.db, closedTask, &evidence); err == nil {
		t.Fatal("closed store read the delivery diagnostic")
	}
	if err := readCleanupDiagnostic(context.Background(), store.db, closedTask, &evidence); err == nil {
		t.Fatal("closed store read the cleanup diagnostic")
	}
	if err := readPreparationDiagnostic(context.Background(), store.db, closedTask.Handle, &evidence); err == nil {
		t.Fatal("closed store read the preparation diagnostic")
	}
	if _, _, err := latestCandidateEvidenceFrom(context.Background(), store.db, closedTask.Handle); err == nil {
		t.Fatal("closed store read reconciled candidate evidence")
	}
	if _, _, err := findTaskCleanupRecord(context.Background(), store.db, "operation-closed-cleanup"); err == nil {
		t.Fatal("closed store found a cleanup operation")
	}
	if _, _, err := findTaskCleanupRecordByTask(context.Background(), store.db, closedTask.Handle); err == nil {
		t.Fatal("closed store found a task cleanup operation")
	}
	if _, err := getTask(context.Background(), store.db, closedTask.Handle); err == nil {
		t.Fatal("closed store read a task")
	}
	if _, err := listTasks(context.Background(), store.db); err == nil {
		t.Fatal("closed store listed tasks")
	}
	if _, err := currentStateVersion(context.Background(), store.db); err == nil {
		t.Fatal("closed store read the state version")
	}
	if _, _, err := store.TaskSnapshot(context.Background()); err == nil {
		t.Fatal("closed store read a task snapshot")
	}
	if _, err := store.ListAcceptedReports(context.Background(), closedTask.Handle); err == nil {
		t.Fatal("closed store listed accepted reports")
	}
}

func TestPreparationIntentReplaysAndConflictsConservatively(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 1, 30, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-intent-coverage", TaskHandle: "task-intent-coverage",
		SubjectDigest: strings.Repeat("b", 64), CreatedAt: now,
	}
	if recorded, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil || recorded != intent {
		t.Fatalf("RecordTaskPreparationIntent(first) = %#v, %v", recorded, err)
	}
	if replayed, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil || replayed != intent {
		t.Fatalf("RecordTaskPreparationIntent(replay) = %#v, %v", replayed, err)
	}
	altered := intent
	altered.SubjectDigest = strings.Repeat("c", 64)
	if _, err := store.RecordTaskPreparationIntent(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("RecordTaskPreparationIntent(altered replay) error = %v", err)
	}
	collision := intent
	collision.OperationID = "operation-intent-collision"
	if _, err := store.RecordTaskPreparationIntent(context.Background(), collision); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("RecordTaskPreparationIntent(task collision) error = %v", err)
	}
	if intents, err := store.ListTaskPreparationIntents(context.Background()); err != nil || len(intents) != 1 || intents[0] != intent {
		t.Fatalf("ListTaskPreparationIntents() = %#v, %v", intents, err)
	}
	if _, err := store.db.Exec(`UPDATE task_preparation_intents SET created_at = 'invalid' WHERE operation_id = ?`, intent.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListTaskPreparationIntents(context.Background()); err == nil {
		t.Fatal("malformed preparation intent was listed")
	}
}

func TestPreparationIntentReplaysCompletedMutationAuthority(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 1, 45, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-completed-preparation-replay", TaskHandle: "task-completed-preparation-replay",
		SubjectDigest: strings.Repeat("f", 64), CreatedAt: now,
	}
	task := storeTask(intent.TaskHandle, 1)
	task.CreatedAt, task.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	operation := completedMutationOperation(
		intent.OperationID, commandPrepareTask, intent.SubjectDigest, task.Handle, task.StateVersion, now,
	)
	if err := store.RecordOperation(context.Background(), operation); err != nil {
		t.Fatal(err)
	}
	replayed, err := store.RecordTaskPreparationIntent(context.Background(), intent)
	if err != nil || replayed != intent {
		t.Fatalf("RecordTaskPreparationIntent(completed mutation) = %#v, %v", replayed, err)
	}
	if _, err := readTaskPreparationIntent(context.Background(), store.db, intent.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("readTaskPreparationIntent(completed mutation) error = %v", err)
	}

	pending := intent
	pending.OperationID = "operation-consume-conflict"
	pending.TaskHandle = "task-consume-conflict"
	if _, err := store.RecordTaskPreparationIntent(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	mutation := application.PreparedTaskMutation{
		OperationID:   pending.OperationID,
		SubjectDigest: strings.Repeat("e", 64),
		Task:          domain.Task{Handle: pending.TaskHandle},
		At:            pending.CreatedAt,
	}
	if err := consumeTaskPreparationIntent(context.Background(), transaction, mutation); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("consumeTaskPreparationIntent(changed digest) error = %v", err)
	}
}

func TestPreparationIntentFailureBoundariesPreserveDurableAuthority(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 1, 50, 0, 0, time.UTC)
	missingTaskIntent := application.TaskPreparationIntent{
		OperationID: "operation-missing-replay-task", TaskHandle: "task-missing-replay-task",
		SubjectDigest: strings.Repeat("1", 64), CreatedAt: now,
	}
	missingTaskOperation := completedMutationOperation(
		missingTaskIntent.OperationID, commandPrepareTask, missingTaskIntent.SubjectDigest,
		missingTaskIntent.TaskHandle, 1, now,
	)
	if err := store.RecordOperation(context.Background(), missingTaskOperation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), missingTaskIntent); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("RecordTaskPreparationIntent(missing replay task) error = %v", err)
	}

	conflictingIntent := missingTaskIntent
	conflictingIntent.OperationID = "operation-conflicting-replay"
	conflictingIntent.SubjectDigest = strings.Repeat("2", 64)
	conflictingOperation := completedMutationOperation(
		conflictingIntent.OperationID, commandPrepareTask, strings.Repeat("3", 64),
		conflictingIntent.TaskHandle, 1, now,
	)
	if err := store.RecordOperation(context.Background(), conflictingOperation); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), conflictingIntent); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("RecordTaskPreparationIntent(conflicting operation) error = %v", err)
	}

	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(context.Background(), `DROP TABLE task_preparation_intents`); err != nil {
		t.Fatal(err)
	}
	if err := consumeTaskPreparationIntent(context.Background(), transaction, application.PreparedTaskMutation{
		OperationID: "operation-unavailable-intent",
	}); err == nil {
		t.Fatal("consumeTaskPreparationIntent accepted an unavailable authority table")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}

	pending := application.TaskPreparationIntent{
		OperationID: "operation-delete-refusal", TaskHandle: "task-delete-refusal",
		SubjectDigest: strings.Repeat("4", 64), CreatedAt: now,
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), pending); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER refuse_intent_delete BEFORE DELETE ON task_preparation_intents
		BEGIN SELECT RAISE(ABORT, 'delete refused'); END`); err != nil {
		t.Fatal(err)
	}
	transaction, err = store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	mutation := application.PreparedTaskMutation{
		OperationID: pending.OperationID, SubjectDigest: pending.SubjectDigest,
		Task: domain.Task{Handle: pending.TaskHandle}, At: pending.CreatedAt,
	}
	if err := consumeTaskPreparationIntent(context.Background(), transaction, mutation); err == nil {
		t.Fatal("consumeTaskPreparationIntent ignored a refused durable delete")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteMutationHelpersClassifyAdapterFailures(t *testing.T) {
	plain := errors.New("adapter failed")
	if err := insertCandidateEvidence(context.Background(), recoveryFailingExecer{err: plain}, candidateEvidenceRow{}); !errors.Is(err, plain) {
		t.Fatalf("insertCandidateEvidence(adapter failure) error = %v", err)
	}
	constraint := codedReconciliationError{code: sqliteConstraintCode}
	if err := insertCandidateEvidence(context.Background(), recoveryFailingExecer{err: constraint}, candidateEvidenceRow{}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("insertCandidateEvidence(constraint) error = %v", err)
	}
	if err := insertTask(context.Background(), recoveryFailingExecer{err: plain}, storeTask("task-adapter-failure", 1)); !errors.Is(err, plain) {
		t.Fatalf("insertTask(adapter failure) error = %v", err)
	}
	if err := taskReconciliationConstraintFailure("reconcile candidate", plain); !errors.Is(err, plain) {
		t.Fatalf("taskReconciliationConstraintFailure(adapter failure) error = %v", err)
	}
}

func TestRuntimeRecoveryRefusalIsIdempotentAndTaskScoped(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 2, 0, 0, 0, time.UTC)
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), "task-missing-coverage", now); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(missing) error = %v", err)
	}
	task := storeTask("task-refusal-coverage", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(time.Minute)); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(first) error = %v", err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(replay) error = %v", err)
	}
	refusals, err := store.ListRuntimeRelayIdentityRefusals(context.Background())
	if err != nil || len(refusals) != 1 || refusals[0].TaskHandle != task.Handle {
		t.Fatalf("ListRuntimeRelayIdentityRefusals() = %#v, %v", refusals, err)
	}
	stored, err := store.GetTask(context.Background(), task.Handle)
	if err != nil || stored.State != domain.TaskUnknown {
		t.Fatalf("GetTask(refused) = %#v, %v", stored, err)
	}

	seed := [32]byte{1}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	missingUpgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle:    task.Handle,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		RelaySeed:     seed,
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(context.Background(), missingUpgrade); err == nil {
		t.Fatal("missing relay upgrade completed")
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(context.Background(), application.RuntimeRelayIdentityUpgrade{}); err == nil {
		t.Fatal("invalid relay upgrade completed")
	}
	if err := store.RefuseRuntimeRelayIdentityUpgrade(context.Background(), application.RuntimeRelayIdentityUpgrade{}, now); err == nil {
		t.Fatal("invalid relay upgrade was refused")
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), "", now); err == nil {
		t.Fatal("invalid task recovery was refused")
	}
	cleaned := storeTask("task-cleaned-refusal-coverage", 2)
	cleaned.State = domain.TaskCleaned
	cleaned.CreatedAt, cleaned.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), cleaned); err != nil {
		t.Fatal(err)
	}
	if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), cleaned.Handle, now.Add(3*time.Minute)); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentTaskRecovery(cleaned) error = %v", err)
	}
}

func TestPreparationRecoveryRefusalBindsExactDurableIntent(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 3, 0, 0, 0, time.UTC)
	intent := application.TaskPreparationIntent{
		OperationID: "operation-refusal-coverage", TaskHandle: "task-preparation-refusal",
		SubjectDigest: strings.Repeat("d", 64), CreatedAt: now,
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	refusedAt := now.Add(time.Minute)
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, refusedAt); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentRecovery() error = %v", err)
	}
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, refusedAt); err != nil {
		t.Fatalf("RefuseRuntimeAttachmentRecovery(replay) error = %v", err)
	}
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, refusedAt.Add(time.Minute)); err == nil {
		t.Fatal("recovery refusal accepted a different durable outcome")
	}
	altered := intent
	altered.SubjectDigest = strings.Repeat("e", 64)
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), altered, refusedAt); err == nil {
		t.Fatal("recovery refusal accepted changed authority")
	}
	refusals, err := store.ListRuntimeAttachmentRecoveryRefusals(context.Background())
	if err != nil || len(refusals) != 1 || refusals[0].OperationID != intent.OperationID {
		t.Fatalf("ListRuntimeAttachmentRecoveryRefusals() = %#v, %v", refusals, err)
	}
	if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), application.TaskPreparationIntent{}, refusedAt); err == nil {
		t.Fatal("invalid preparation recovery was refused")
	}
	if _, err := store.db.Exec(`UPDATE runtime_attachment_recovery_refusals SET refused_at = 'invalid' WHERE operation_id = ?`, intent.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRuntimeAttachmentRecoveryRefusals(context.Background()); err == nil {
		t.Fatal("malformed preparation refusal was listed")
	}
}

func TestRelayUpgradeReadersRejectMalformedDurableRows(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_upgrades(task_handle, relay_identity, relay_seed)
		VALUES ('task-malformed-upgrade', ?, X'01')`, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err == nil {
		t.Fatal("malformed relay upgrade was listed")
	}
	if _, err := store.db.Exec(`DELETE FROM runtime_relay_identity_upgrades`); err != nil {
		t.Fatal(err)
	}
	invalidSeed := [32]byte{9}
	if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_upgrades(task_handle, relay_identity, relay_seed)
		VALUES ('task-invalid-upgrade-authority', ?, ?)`, strings.Repeat("a", 64), invalidSeed[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err == nil {
		t.Fatal("relay upgrade with inconsistent identity was listed")
	}
	if _, err := store.db.Exec(`DELETE FROM runtime_relay_identity_upgrades`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 5, 0, 0, 0, time.UTC)
	task := storeTask("task-valid-relay-upgrade", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	seed := [32]byte{2}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle: task.Handle, RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)), RelaySeed: seed,
	}
	insertUpgrade := func() {
		t.Helper()
		if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_upgrades(task_handle, relay_identity, relay_seed)
			VALUES (?, ?, ?)`, upgrade.TaskHandle, upgrade.RelayIdentity, upgrade.RelaySeed[:]); err != nil {
			t.Fatal(err)
		}
	}
	insertUpgrade()
	if upgrades, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err != nil ||
		len(upgrades) != 1 || upgrades[0] != upgrade {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades(valid) = %#v, %v", upgrades, err)
	}
	if err := store.CompleteRuntimeRelayIdentityUpgrade(context.Background(), upgrade); err != nil {
		t.Fatalf("CompleteRuntimeRelayIdentityUpgrade() error = %v", err)
	}
	insertUpgrade()
	if err := store.RefuseRuntimeRelayIdentityUpgrade(context.Background(), upgrade, now.Add(time.Minute)); err != nil {
		t.Fatalf("RefuseRuntimeRelayIdentityUpgrade() error = %v", err)
	}
	if upgrades, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err != nil || len(upgrades) != 0 {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades(refused) = %#v, %v", upgrades, err)
	}
	if refusals, err := store.ListRuntimeRelayIdentityRefusals(context.Background()); err != nil ||
		len(refusals) != 1 || refusals[0].TaskHandle != task.Handle {
		t.Fatalf("ListRuntimeRelayIdentityRefusals(valid) = %#v, %v", refusals, err)
	}
	if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_refusals(task_handle, reason)
		VALUES ('task-malformed-refusal', 'invalid')`); err == nil {
		t.Fatal("database accepted an invalid refusal reason")
	}
	if _, err := store.db.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_refusals(task_handle, reason)
		VALUES ('task-malformed-refusal', 'invalid')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListRuntimeRelayIdentityRefusals(context.Background()); err == nil {
		t.Fatal("malformed relay refusal was listed")
	}
	if _, err := runtimeRelayIdentityRefusalExists(context.Background(), store.db, "task-malformed-refusal"); err == nil {
		t.Fatal("malformed relay refusal was accepted as task authority")
	}
	if _, err := store.ListRuntimeAttachmentRecoveryRefusals(context.Background()); err != nil {
		t.Fatalf("empty recovery refusal list error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ListRuntimeAttachmentRecoveryRefusals(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRuntimeAttachmentRecoveryRefusals(canceled) error = %v", err)
	}
}

func TestRuntimeRecoveryMutationFailuresRollbackTaskAuthority(t *testing.T) {
	now := time.Date(2026, time.August, 17, 5, 15, 0, 0, time.UTC)
	openStore := func(t *testing.T) *Store {
		t.Helper()
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	validUpgrade := func(taskHandle string, seedByte byte) application.RuntimeRelayIdentityUpgrade {
		seed := [32]byte{seedByte}
		privateKey := ed25519.NewKeyFromSeed(seed[:])
		return application.RuntimeRelayIdentityUpgrade{
			TaskHandle: taskHandle, RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)), RelaySeed: seed,
		}
	}
	t.Run("preparation authority missing", func(t *testing.T) {
		store := openStore(t)
		intent := application.TaskPreparationIntent{
			OperationID: "operation-missing-recovery-intent", TaskHandle: "task-missing-recovery-intent",
			SubjectDigest: strings.Repeat("5", 64), CreatedAt: now,
		}
		if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, now.Add(time.Minute)); !errors.Is(err, application.ErrNotFound) {
			t.Fatalf("RefuseRuntimeAttachmentRecovery(missing intent) error = %v", err)
		}
	})
	t.Run("preparation refusal insert rejected", func(t *testing.T) {
		store := openStore(t)
		intent := application.TaskPreparationIntent{
			OperationID: "operation-recovery-insert-refusal", TaskHandle: "task-recovery-insert-refusal",
			SubjectDigest: strings.Repeat("6", 64), CreatedAt: now,
		}
		if _, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_recovery_refusal_insert
			BEFORE INSERT ON runtime_attachment_recovery_refusals
			BEGIN SELECT RAISE(ABORT, 'insert refused'); END`); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeAttachmentRecovery(context.Background(), intent, now.Add(time.Minute)); err == nil {
			t.Fatal("preparation recovery ignored a refused authority insert")
		}
	})
	t.Run("relay refusal table unavailable", func(t *testing.T) {
		store := openStore(t)
		if _, err := store.db.Exec(`DROP TABLE runtime_relay_identity_refusals`); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeRelayIdentityUpgrade(
			context.Background(), validUpgrade("task-unavailable-relay-refusal", 10), now,
		); err == nil {
			t.Fatal("relay upgrade refusal accepted an unavailable refusal table")
		}
	})
	t.Run("relay upgrade delete rejected", func(t *testing.T) {
		store := openStore(t)
		task := storeTask("task-relay-delete-refusal", 1)
		task.State = domain.TaskCleaned
		task.CreatedAt, task.UpdatedAt = now, now
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		upgrade := validUpgrade(task.Handle, 11)
		if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_upgrades(task_handle, relay_identity, relay_seed)
			VALUES (?, ?, ?)`, upgrade.TaskHandle, upgrade.RelayIdentity, upgrade.RelaySeed[:]); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_relay_upgrade_delete
			BEFORE DELETE ON runtime_relay_identity_upgrades
			BEGIN SELECT RAISE(ABORT, 'delete refused'); END`); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeRelayIdentityUpgrade(context.Background(), upgrade, now.Add(time.Minute)); err == nil {
			t.Fatal("relay upgrade refusal ignored a refused durable delete")
		}
	})
	t.Run("refusal table unavailable", func(t *testing.T) {
		store := openStore(t)
		if _, err := store.db.Exec(`DROP TABLE runtime_relay_identity_refusals`); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeAttachmentTaskRecovery(
			context.Background(), "task-unavailable-refusal-table", now,
		); err == nil {
			t.Fatal("runtime recovery accepted an unavailable refusal table")
		}
	})
	t.Run("task state cannot reconcile", func(t *testing.T) {
		store := openStore(t)
		task := storeTask("task-regressive-refusal", 1)
		task.CreatedAt, task.UpdatedAt = now, now.Add(2*time.Minute)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(time.Minute)); err == nil {
			t.Fatal("runtime recovery accepted a regressive reconciliation time")
		}
	})
	t.Run("task update refused", func(t *testing.T) {
		store := openStore(t)
		task := storeTask("task-update-refusal", 1)
		task.CreatedAt, task.UpdatedAt = now, now
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_task_update BEFORE UPDATE ON tasks
			BEGIN SELECT RAISE(ABORT, 'update refused'); END`); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(time.Minute)); err == nil {
			t.Fatal("runtime recovery ignored a refused task update")
		}
	})
	t.Run("state version exhausted", func(t *testing.T) {
		store := openStore(t)
		task := storeTask("task-exhausted-refusal", math.MaxInt64)
		task.CreatedAt, task.UpdatedAt = now, now
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(time.Minute)); err == nil {
			t.Fatal("runtime recovery accepted an exhausted state version")
		}
	})
	t.Run("refusal insert rejected", func(t *testing.T) {
		store := openStore(t)
		task := storeTask("task-insert-refusal", 1)
		task.State = domain.TaskCleaned
		task.CreatedAt, task.UpdatedAt = now, now
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`CREATE TRIGGER refuse_runtime_refusal_insert
			BEFORE INSERT ON runtime_relay_identity_refusals
			BEGIN SELECT RAISE(ABORT, 'insert refused'); END`); err != nil {
			t.Fatal(err)
		}
		if err := store.RefuseRuntimeAttachmentTaskRecovery(context.Background(), task.Handle, now.Add(time.Minute)); err == nil {
			t.Fatal("runtime recovery ignored a refused authority insert")
		}
	})
}

func TestRelayUpgradeRefusalRejectsChangedDurableAuthority(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 17, 5, 30, 0, 0, time.UTC)
	task := storeTask("task-changed-relay-upgrade", 1)
	task.CreatedAt, task.UpdatedAt = now, now
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
		t.Fatal(err)
	}
	seed := [32]byte{3}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	durable := application.RuntimeRelayIdentityUpgrade{
		TaskHandle: task.Handle, RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)), RelaySeed: seed,
	}
	if _, err := store.db.Exec(`INSERT INTO runtime_relay_identity_upgrades(task_handle, relay_identity, relay_seed)
		VALUES (?, ?, ?)`, durable.TaskHandle, durable.RelayIdentity, durable.RelaySeed[:]); err != nil {
		t.Fatal(err)
	}
	presentedSeed := [32]byte{4}
	presentedKey := ed25519.NewKeyFromSeed(presentedSeed[:])
	presented := application.RuntimeRelayIdentityUpgrade{
		TaskHandle:    task.Handle,
		RelayIdentity: hex.EncodeToString(presentedKey.Public().(ed25519.PublicKey)),
		RelaySeed:     presentedSeed,
	}
	if err := store.RefuseRuntimeRelayIdentityUpgrade(context.Background(), presented, now.Add(time.Minute)); err == nil {
		t.Fatal("relay upgrade refusal accepted changed durable authority")
	}
	if upgrades, err := store.ListRuntimeRelayIdentityUpgrades(context.Background()); err != nil || len(upgrades) != 1 || upgrades[0] != durable {
		t.Fatalf("ListRuntimeRelayIdentityUpgrades(rollback) = %#v, %v", upgrades, err)
	}
	if refusals, err := store.ListRuntimeRelayIdentityRefusals(context.Background()); err != nil || len(refusals) != 0 {
		t.Fatalf("ListRuntimeRelayIdentityRefusals(rollback) = %#v, %v", refusals, err)
	}
}
