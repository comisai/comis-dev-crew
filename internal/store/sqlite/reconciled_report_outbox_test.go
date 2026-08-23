package sqlite

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestReconciledReportOutbox_BackfillsExactSettledCandidateWithoutWorkerReport(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "reconciled-backfill.db")
	store, task := deliveredReconciledCandidateFixture(t, databasePath, "task-reconciled-backfill")
	if _, err := store.db.Exec("UPDATE tasks SET state = 'cleaned' WHERE handle = ?", task.Handle); err != nil {
		t.Fatalf("settle reconciled task cleanup: %v", err)
	}
	if _, err := store.db.Exec(`DROP TABLE comis_reconciled_report_outbox;
		DELETE FROM schema_migrations WHERE version = 42`); err != nil {
		t.Fatalf("remove reconciled report migration: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(backfill) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	delivery, found, err := reopened.NextComisReport(context.Background())
	if err != nil || !found || delivery.TaskHandle != task.Handle ||
		delivery.Kind != domain.ReportCandidateComplete || len(delivery.ArtifactRefs) != 2 {
		t.Fatalf("NextComisReport(backfill) = %#v, %t, %v", delivery, found, err)
	}
	var workerReports int
	if err := reopened.db.QueryRow(`SELECT COUNT(*) FROM reports
		WHERE task_handle = ? AND kind = 'candidate_complete'`, task.Handle).Scan(&workerReports); err != nil {
		t.Fatal(err)
	}
	if workerReports != 0 {
		t.Fatalf("worker candidate reports = %d, want zero", workerReports)
	}
}

func TestReconciledReportOutbox_BackfillRefusesIncompleteEvidenceAuthority(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "reconciled-incomplete.db")
	store, task := deliveredReconciledCandidateFixture(t, databasePath, "task-reconciled-incomplete")
	if _, err := store.db.Exec(`DELETE FROM comis_evidence_outbox
		WHERE task_handle = ? AND evidence_ref = (
			SELECT MAX(evidence_ref) FROM comis_evidence_outbox WHERE task_handle = ?
		)`, task.Handle, task.Handle); err != nil {
		t.Fatalf("damage reconciled report authority: %v", err)
	}
	if _, err := store.db.Exec(`DROP TABLE comis_reconciled_report_outbox;
		DELETE FROM schema_migrations WHERE version = 42`); err != nil {
		t.Fatalf("remove reconciled report migration: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if reopened, err := Open(context.Background(), databasePath); err == nil {
		_ = reopened.Close()
		t.Fatal("Open(incomplete backfill) error = nil")
	}
}

func TestReconciledReportOutbox_PreservesTerminalProjectionAfterCleanup(t *testing.T) {
	store, task := deliveredReconciledCandidateFixture(t,
		filepath.Join(canonicalTempDir(t), "reconciled-cleaned.db"), "task-reconciled-cleaned")
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec("UPDATE tasks SET state = 'cleaned' WHERE handle = ?", task.Handle); err != nil {
		t.Fatalf("settle task cleanup state: %v", err)
	}
	delivery, found, err := store.NextComisReport(context.Background())
	if err != nil || !found || delivery.TaskHandle != task.Handle || delivery.Kind != domain.ReportCandidateComplete {
		t.Fatalf("NextComisReport(cleaned reconciliation) = %#v, %t, %v", delivery, found, err)
	}
}

func TestReconciledReportOutbox_RefusesAlteredDuplicateAndUnavailableStorage(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "reconciled-boundaries.db")
	store, task := deliveredReconciledCandidateFixture(t, databasePath, "task-reconciled-boundaries")
	evidence, judgment, err := store.LatestCandidateEvidence(context.Background(), task.Handle)
	if err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("LatestCandidateEvidence() = %#v, %v", judgment, err)
	}
	durableTask, err := store.GetTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if _, err := store.db.Exec("UPDATE comis_reconciled_report_outbox SET summary = '' WHERE task_handle = ?", task.Handle); err != nil {
		t.Fatalf("alter reconciled report summary: %v", err)
	}
	if _, found, err := store.NextComisReport(context.Background()); err == nil || found {
		t.Fatalf("NextComisReport(invalid summary) = found %t, error %v", found, err)
	}
	if _, err := store.db.Exec("UPDATE comis_reconciled_report_outbox SET summary = ? WHERE task_handle = ?", reconciledCandidateSummary, task.Handle); err != nil {
		t.Fatalf("restore reconciled report summary: %v", err)
	}
	delivery, found, err := store.NextComisReport(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisReport() = %#v, %t, %v", delivery, found, err)
	}
	reportDeliveredAt := time.Now().UTC()
	acknowledgement := application.ComisReportAcknowledgement{
		ManagedRunID: delivery.ManagedRunID, ServiceReportID: delivery.ServiceReportID,
		AcceptedSequence: 1, RetainedUntil: reportDeliveredAt.Add(time.Hour),
	}
	if _, err := store.db.Exec(`CREATE TRIGGER refuse_reconciled_report_ack
		BEFORE UPDATE ON comis_reconciled_report_outbox
		BEGIN SELECT RAISE(FAIL, 'reconciled report acknowledgement unavailable'); END`); err != nil {
		t.Fatalf("install reconciled report acknowledgement refusal: %v", err)
	}
	if err := store.MarkComisReportDelivered(context.Background(), delivery.OperationID, acknowledgement, reportDeliveredAt); err == nil {
		t.Fatal("MarkComisReportDelivered(refused acknowledgement) error = nil")
	}
	if _, err := store.db.Exec("DROP TRIGGER refuse_reconciled_report_ack"); err != nil {
		t.Fatalf("drop reconciled report acknowledgement refusal: %v", err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	if err := insertReconciledComisReport(context.Background(), transaction, durableTask, evidence, 0); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("insertReconciledComisReport(invalid authority) error = %v, want precondition", err)
	}
	if err := insertReconciledComisReport(context.Background(), transaction, durableTask, evidence, durableTask.StateVersion); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("insertReconciledComisReport(duplicate) error = %v, want conflict", err)
	}
	if _, err := updateComisReportAcknowledgement(context.Background(), transaction, reportOutboxSource("invalid"),
		delivery.OperationID, acknowledgement, reportDeliveredAt); err == nil {
		t.Fatal("updateComisReportAcknowledgement(invalid source) error = nil")
	}
	if _, err := transaction.Exec("DROP TABLE comis_reconciled_report_outbox"); err != nil {
		t.Fatalf("drop reconciled report outbox: %v", err)
	}
	if err := insertReconciledComisReport(context.Background(), transaction, durableTask, evidence, durableTask.StateVersion); err == nil {
		t.Fatal("insertReconciledComisReport(unavailable outbox) error = nil")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatalf("Rollback() error = %v", err)
	}

	if _, err := store.db.Exec(`UPDATE candidate_evidence
		SET outcome = 'rejected', reason = 'validation_failed' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatalf("alter candidate judgment: %v", err)
	}
	if err := store.backfillReconciledComisReport(context.Background(), task.Handle); err == nil {
		t.Fatal("backfillReconciledComisReport(rejected) error = nil")
	}
	if _, err := store.db.Exec(`UPDATE candidate_evidence
		SET outcome = 'accepted', reason = 'evidence_accepted' WHERE task_handle = ?`, task.Handle); err != nil {
		t.Fatalf("restore candidate judgment: %v", err)
	}
	if _, err := store.db.Exec("DELETE FROM comis_reconciled_report_outbox WHERE task_handle = ?", task.Handle); err != nil {
		t.Fatalf("remove reconciled report row: %v", err)
	}
	if _, err := store.db.Exec(`CREATE TRIGGER refuse_reconciled_report_backfill
		BEFORE INSERT ON comis_reconciled_report_outbox
		BEGIN SELECT RAISE(FAIL, 'reconciled report unavailable'); END`); err != nil {
		t.Fatalf("install reconciled report refusal: %v", err)
	}
	if err := store.backfillReconciledComisReport(context.Background(), task.Handle); err == nil {
		t.Fatal("backfillReconciledComisReport(refused insert) error = nil")
	}
	if _, err := store.db.Exec(`DROP TRIGGER refuse_reconciled_report_backfill;
		DROP TABLE comis_evidence_outbox`); err != nil {
		t.Fatalf("remove evidence storage: %v", err)
	}
	if err := store.backfillReconciledComisReport(context.Background(), task.Handle); err == nil {
		t.Fatal("backfillReconciledComisReport(unavailable evidence) error = nil")
	}
	if err := store.backfillReconciledComisReport(context.Background(), "task-reconciled-missing"); err == nil {
		t.Fatal("backfillReconciledComisReport(missing task) error = nil")
	}
	if _, err := store.db.Exec("DROP TABLE comis_reconciled_report_outbox"); err != nil {
		t.Fatalf("drop reconciled report outbox: %v", err)
	}
	if _, _, err := store.NextComisReport(context.Background()); err == nil {
		t.Fatal("NextComisReport(unavailable outbox) error = nil")
	}
	if err := store.MarkComisReportDelivered(context.Background(), delivery.OperationID, acknowledgement, reportDeliveredAt); err == nil {
		t.Fatal("MarkComisReportDelivered(unavailable outbox) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.backfillReconciledComisReports(context.Background()); err == nil {
		t.Fatal("backfillReconciledComisReports(closed) error = nil")
	}
	if err := store.backfillReconciledComisReport(context.Background(), task.Handle); err == nil {
		t.Fatal("backfillReconciledComisReport(closed) error = nil")
	}
}

func TestReconciledCandidateDelivery_RefusesCorruptAndUnwritableTerminalTransitions(t *testing.T) {
	store, task := deliveredReconciledCandidateFixture(t,
		filepath.Join(canonicalTempDir(t), "reconciled-delivery-boundaries.db"),
		"task-reconciled-delivery-boundaries")
	t.Cleanup(func() { _ = store.Close() })
	current, err := store.GetTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	var evidenceOperationID string
	if err := store.db.QueryRow(`SELECT MIN(operation_id) FROM comis_evidence_outbox
		WHERE task_handle = ?`, task.Handle).Scan(&evidenceOperationID); err != nil {
		t.Fatalf("read evidence operation: %v", err)
	}

	tests := []struct {
		name      string
		prepare   func(*testing.T, execer)
		delivered time.Time
	}{
		{name: "missing report ledger", delivered: current.UpdatedAt.Add(time.Minute), prepare: func(t *testing.T, target execer) {
			if _, err := target.ExecContext(context.Background(), "DROP TABLE reports"); err != nil {
				t.Fatalf("drop report ledger: %v", err)
			}
		}},
		{name: "regressive delivery time", delivered: current.UpdatedAt.Add(-time.Minute), prepare: func(*testing.T, execer) {}},
		{name: "exhausted state version", delivered: current.UpdatedAt.Add(time.Minute), prepare: func(t *testing.T, target execer) {
			if _, err := target.ExecContext(context.Background(), "UPDATE tasks SET state_version = ? WHERE handle = ?", math.MaxInt64, task.Handle); err != nil {
				t.Fatalf("exhaust task state version: %v", err)
			}
		}},
		{name: "refused task update", delivered: current.UpdatedAt.Add(time.Minute), prepare: func(t *testing.T, target execer) {
			if _, err := target.ExecContext(context.Background(), `CREATE TRIGGER refuse_reconciled_delivery_update
				BEFORE UPDATE ON tasks
				BEGIN SELECT RAISE(FAIL, 'reconciled delivery update unavailable'); END`); err != nil {
				t.Fatalf("install task update refusal: %v", err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction, err := store.db.BeginTx(context.Background(), nil)
			if err != nil {
				t.Fatalf("BeginTx() error = %v", err)
			}
			defer func() { _ = transaction.Rollback() }()
			if _, err := transaction.Exec("UPDATE tasks SET state = 'candidate_complete' WHERE handle = ?", task.Handle); err != nil {
				t.Fatalf("restore candidate state: %v", err)
			}
			test.prepare(t, transaction)
			if err := completeReconciledCandidateDelivery(context.Background(), transaction,
				evidenceOperationID, test.delivered); err == nil {
				t.Fatal("completeReconciledCandidateDelivery() error = nil")
			}
		})
	}
}

func deliveredReconciledCandidateFixture(t *testing.T, databasePath, taskHandle string) (*Store, domain.Task) {
	t.Helper()
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := candidateEvidenceTask(t, taskHandle)
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	evidence := candidateEvidence(t, task, strings.Repeat("d", 40))
	insertEvidenceReconciliation(t, store, task, evidence.Bundle().HeadRevision)
	publications := candidateEvidencePublications(t, task, evidence)
	judgedAt := evidence.Bundle().ProducedAt
	if _, judgment, err := store.CommitCandidateEvidence(context.Background(), task.Handle, evidence,
		[]string{"unit"}, []string{"ci/unit"}, judgedAt, publications); err != nil || judgment.Outcome != domain.CandidateAccepted {
		t.Fatalf("CommitCandidateEvidence() = %#v, %v", judgment, err)
	}
	for index := range publications {
		delivery, found, err := store.NextComisEvidence(context.Background())
		if err != nil || !found {
			t.Fatalf("NextComisEvidence(%d) = %#v, %t, %v", index, delivery, found, err)
		}
		deliveredAt := judgedAt.Add(time.Duration(index+1) * time.Minute)
		retainedUntil := deliveredAt.Add(time.Hour)
		if err := store.MarkComisEvidenceDelivered(context.Background(), delivery.OperationID,
			application.ComisEvidenceAcknowledgement{
				ManagedRunID: delivery.ManagedRunID, EvidenceRef: delivery.EvidenceRef,
				ContentHash: delivery.ContentHash, VerificationLevel: delivery.VerificationLevel,
				RetainedUntil: &retainedUntil,
			}, deliveredAt); err != nil {
			t.Fatalf("MarkComisEvidenceDelivered(%d) error = %v", index, err)
		}
	}
	return store, task
}
