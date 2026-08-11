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

func TestComisReportOutbox_IsAtomicReplaySafeAndRestartDurable(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, task := openReportFixture(t, databasePath)
	acceptedAt := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	report := sqliteWorkerReport(task, "report-outbox-0001", domain.ReportProgress)
	mutation := directReportMutation(task, report, acceptedAt)
	receipt, err := store.CommitReport(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	pending, found, err := store.NextComisReport(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisReport() = %#v, %t, %v", pending, found, err)
	}
	if pending.TaskHandle != task.Handle || pending.LocalReportID != report.LocalReportID ||
		pending.ManagedRunID != task.ManagedRunID || pending.Kind != report.Kind ||
		pending.Summary != report.Summary || pending.StateVersion != receipt.StateVersion {
		t.Fatalf("pending report = %#v, want exact accepted report and binding", pending)
	}
	if !strings.HasPrefix(pending.OperationID, "report-") || !strings.HasPrefix(pending.ServiceReportID, "service-report-") {
		t.Fatalf("stable delivery identities = %q/%q", pending.OperationID, pending.ServiceReportID)
	}
	replayReceipt, err := store.CommitReport(context.Background(), mutation)
	if err != nil || replayReceipt != receipt {
		t.Fatalf("CommitReport(replay) = %#v, %v, want %#v", replayReceipt, err, receipt)
	}
	replayedPending, found, err := store.NextComisReport(context.Background())
	if err != nil || !found || !reflect.DeepEqual(replayedPending, pending) {
		t.Fatalf("NextComisReport(replay) = %#v, %t, %v, want one %#v", replayedPending, found, err, pending)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restartedPending, found, err := reopened.NextComisReport(context.Background())
	if err != nil || !found || !reflect.DeepEqual(restartedPending, pending) {
		t.Fatalf("NextComisReport(restart) = %#v, %t, %v, want %#v", restartedPending, found, err, pending)
	}
	deliveredAt := acceptedAt.Add(time.Minute)
	ack := application.ComisReportAcknowledgement{
		ManagedRunID: pending.ManagedRunID, ServiceReportID: pending.ServiceReportID,
		AcceptedSequence: 11, RetainedUntil: deliveredAt.Add(24 * time.Hour),
	}
	wrongIdentity := ack
	wrongIdentity.ManagedRunID = "managed-run-other"
	if err := reopened.MarkComisReportDelivered(context.Background(), pending.OperationID, wrongIdentity, deliveredAt); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("MarkComisReportDelivered(wrong identity) error = %v, want ErrConflict", err)
	}
	if err := reopened.MarkComisReportDelivered(context.Background(), pending.OperationID, ack, deliveredAt); err != nil {
		t.Fatalf("MarkComisReportDelivered() error = %v", err)
	}
	if err := reopened.MarkComisReportDelivered(context.Background(), pending.OperationID, ack, deliveredAt); err != nil {
		t.Fatalf("MarkComisReportDelivered(replay) error = %v", err)
	}
	if next, found, err := reopened.NextComisReport(context.Background()); err != nil || found {
		t.Fatalf("NextComisReport(delivered) = %#v, %t, %v, want empty", next, found, err)
	}
	altered := ack
	altered.AcceptedSequence++
	if err := reopened.MarkComisReportDelivered(context.Background(), pending.OperationID, altered, deliveredAt); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("MarkComisReportDelivered(altered) error = %v, want ErrConflict", err)
	}
}

func TestComisReportOutbox_HoldsCandidateReportUntilEvidenceDeliveryCompletes(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	defer func() { _ = store.Close() }()
	report := sqliteWorkerReport(task, "report-candidate-held", domain.ReportCandidateComplete)
	if _, err := store.CommitReport(
		context.Background(), directReportMutation(task, report, task.UpdatedAt.Add(time.Minute)),
	); err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if next, found, err := store.NextComisReport(context.Background()); err != nil || found {
		t.Fatalf("NextComisReport(candidate before evidence) = %#v, %t, %v, want held", next, found, err)
	}
	validating, err := store.GetTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	sealed := candidateEvidence(t, validating, strings.Repeat("b", 40))
	publications := candidateEvidencePublications(t, validating, sealed)
	judgedAt := validating.UpdatedAt.Add(5 * time.Minute)
	if _, _, err := store.CommitCandidateEvidence(
		context.Background(), validating.Handle, sealed, []string{"unit"}, []string{"ci/unit"}, judgedAt, publications,
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}
	if next, found, err := store.NextComisReport(context.Background()); err != nil || found {
		t.Fatalf("NextComisReport(candidate before acknowledgements) = %#v, %t, %v, want held", next, found, err)
	}
	for range publications {
		evidence, found, err := store.NextComisEvidence(context.Background())
		if err != nil || !found {
			t.Fatalf("NextComisEvidence() = %#v, %t, %v", evidence, found, err)
		}
		deliveredAt := judgedAt.Add(time.Minute)
		retainedUntil := deliveredAt.Add(24 * time.Hour)
		if err := store.MarkComisEvidenceDelivered(context.Background(), evidence.OperationID, application.ComisEvidenceAcknowledgement{
			ManagedRunID: evidence.ManagedRunID, EvidenceRef: evidence.EvidenceRef,
			ContentHash: evidence.ContentHash, VerificationLevel: evidence.VerificationLevel,
			RetainedUntil: &retainedUntil,
		}, deliveredAt); err != nil {
			t.Fatalf("MarkComisEvidenceDelivered() error = %v", err)
		}
	}
	pending, found, err := store.NextComisReport(context.Background())
	if err != nil || !found || !reflect.DeepEqual(pending.ArtifactRefs, []string{
		publications[0].EvidenceRef, publications[1].EvidenceRef,
	}) {
		t.Fatalf("NextComisReport(candidate with evidence) = %#v, %t, %v", pending, found, err)
	}
}

func TestComisReportOutbox_ValidationAndCorruptionFailClosed(t *testing.T) {
	observed := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	validDelivery := application.ComisReportDelivery{
		OperationID: "report-0001", TaskHandle: "task-0001", LocalReportID: "local-report-0001",
		ManagedRunID: "managed-run-0001", ServiceReportID: "service-report-0001",
		Kind: domain.ReportProgress, Summary: "bounded report", WorkerObservedAt: &observed, StateVersion: 1,
	}
	deliveryTests := []struct {
		name   string
		mutate func(*application.ComisReportDelivery)
	}{
		{name: "operation", mutate: func(value *application.ComisReportDelivery) { value.OperationID = "bad operation" }},
		{name: "task", mutate: func(value *application.ComisReportDelivery) { value.TaskHandle = "../bad" }},
		{name: "local report", mutate: func(value *application.ComisReportDelivery) { value.LocalReportID = "bad report" }},
		{name: "service report", mutate: func(value *application.ComisReportDelivery) { value.ServiceReportID = "bad report" }},
		{name: "managed run", mutate: func(value *application.ComisReportDelivery) { value.ManagedRunID = "bad run" }},
		{name: "state version", mutate: func(value *application.ComisReportDelivery) { value.StateVersion = 0 }},
		{name: "sparse report", mutate: func(value *application.ComisReportDelivery) { value.Kind = "invented" }},
	}
	for _, test := range deliveryTests {
		value := validDelivery
		test.mutate(&value)
		if err := validateComisDelivery(value); err == nil {
			t.Errorf("validateComisDelivery(%s) error = nil", test.name)
		}
	}
	deliveredAt := observed.Add(time.Minute)
	validAck := application.ComisReportAcknowledgement{
		ManagedRunID: "managed-run-0001", ServiceReportID: "service-report-0001",
		AcceptedSequence: 1, RetainedUntil: deliveredAt.Add(time.Hour),
	}
	ackTests := []struct {
		name      string
		operation string
		ack       application.ComisReportAcknowledgement
		delivered time.Time
	}{
		{name: "operation", operation: "bad operation", ack: validAck, delivered: deliveredAt},
		{name: "managed run", operation: "report-0001", ack: func() application.ComisReportAcknowledgement {
			value := validAck
			value.ManagedRunID = "bad run"
			return value
		}(), delivered: deliveredAt},
		{name: "service report", operation: "report-0001", ack: func() application.ComisReportAcknowledgement {
			value := validAck
			value.ServiceReportID = "bad report"
			return value
		}(), delivered: deliveredAt},
		{name: "sequence", operation: "report-0001", ack: func() application.ComisReportAcknowledgement {
			value := validAck
			value.AcceptedSequence = 0
			return value
		}(), delivered: deliveredAt},
		{name: "delivered time", operation: "report-0001", ack: validAck, delivered: time.Time{}},
		{name: "retention", operation: "report-0001", ack: func() application.ComisReportAcknowledgement {
			value := validAck
			value.RetainedUntil = deliveredAt
			return value
		}(), delivered: deliveredAt},
	}
	for _, test := range ackTests {
		if err := validateComisAcknowledgement(test.operation, test.ack, test.delivered); err == nil {
			t.Errorf("validateComisAcknowledgement(%s) error = nil", test.name)
		}
	}

	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	report := sqliteWorkerReport(task, "report-corrupt-outbox", domain.ReportProgress)
	if _, err := store.CommitReport(context.Background(), directReportMutation(task, report, observed)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE reports SET worker_observed_at = 'not-a-time' WHERE task_handle = ?", task.Handle); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NextComisReport(context.Background()); err == nil {
		t.Fatal("NextComisReport(corrupt observation) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.NextComisReport(context.Background()); err == nil {
		t.Fatal("NextComisReport(closed) error = nil")
	}
	if err := store.MarkComisReportDelivered(context.Background(), "report-0001", validAck, deliveredAt); err == nil {
		t.Fatal("MarkComisReportDelivered(closed) error = nil")
	}
}

func TestComisReportOutbox_MissingAndDuplicateIdentitiesFailClosed(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	acceptedAt := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	report := sqliteWorkerReport(task, "report-duplicate-outbox", domain.ReportProgress)
	mutation := directReportMutation(task, report, acceptedAt)
	if _, err := store.CommitReport(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	pending, found, err := store.NextComisReport(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisReport() = %#v, %t, %v", pending, found, err)
	}
	ack := application.ComisReportAcknowledgement{
		ManagedRunID: pending.ManagedRunID, ServiceReportID: pending.ServiceReportID,
		AcceptedSequence: 1, RetainedUntil: acceptedAt.Add(time.Hour),
	}
	if err := store.MarkComisReportDelivered(context.Background(), "report-missing", ack, acceptedAt.Add(time.Minute)); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("MarkComisReportDelivered(missing) error = %v", err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	accepted, found, err := findAcceptedReport(context.Background(), transaction, task.Handle, report.LocalReportID)
	if err != nil || !found {
		t.Fatalf("findAcceptedReport() = %#v, %t, %v", accepted, found, err)
	}
	if err := insertComisReport(context.Background(), transaction, task, accepted); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("insertComisReport(duplicate) error = %v, want ErrConflict", err)
	}
	wrongTask := task
	wrongTask.Handle = "task-other"
	if err := insertComisReport(context.Background(), transaction, wrongTask, accepted); err == nil {
		t.Fatal("insertComisReport(wrong task) error = nil")
	}
}

func TestComisReportOutbox_MigrationBackfillsPreviouslyAcceptedReports(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, task := openReportFixture(t, databasePath)
	report := sqliteWorkerReport(task, "report-before-outbox", domain.ReportProgress)
	mutation := directReportMutation(task, report, task.UpdatedAt.Add(time.Minute))
	if _, err := store.CommitReport(context.Background(), mutation); err != nil {
		t.Fatal(err)
	}
	original, found, err := store.NextComisReport(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisReport() = %#v, %t, %v", original, found, err)
	}
	if _, err := store.db.Exec("DROP TABLE comis_report_outbox; DELETE FROM schema_migrations WHERE version = 7"); err != nil {
		t.Fatalf("simulate version 6 database: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(upgrade) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	backfilled, found, err := reopened.NextComisReport(context.Background())
	if err != nil || !found || !reflect.DeepEqual(backfilled, original) {
		t.Fatalf("NextComisReport(backfill) = %#v, %t, %v, want %#v", backfilled, found, err, original)
	}
}

func TestComisReportOutbox_MigrationRejectsPartialAndUnboundState(t *testing.T) {
	t.Run("partial schema", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		if _, err := store.db.Exec("DELETE FROM schema_migrations WHERE version = 7"); err != nil {
			t.Fatal(err)
		}
		if err := store.applyComisReportOutboxMigration(context.Background()); err == nil {
			t.Fatal("applyComisReportOutboxMigration(partial) error = nil")
		}
	})

	t.Run("unbound prior report", func(t *testing.T) {
		store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
		t.Cleanup(func() { _ = store.Close() })
		report := sqliteWorkerReport(task, "report-unbound-upgrade", domain.ReportProgress)
		if _, err := store.CommitReport(context.Background(), directReportMutation(task, report, task.UpdatedAt.Add(time.Minute))); err != nil {
			t.Fatal(err)
		}
		if _, err := store.db.Exec(`DROP TABLE comis_report_outbox;
            DELETE FROM schema_migrations WHERE version = 7;
            UPDATE tasks SET managed_run_id = '' WHERE handle = ?`, task.Handle); err != nil {
			t.Fatal(err)
		}
		if err := store.applyComisReportOutboxMigration(context.Background()); err == nil {
			t.Fatal("applyComisReportOutboxMigration(unbound) error = nil")
		}
	})

	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.applyComisReportOutboxMigration(context.Background()); err == nil {
		t.Fatal("applyComisReportOutboxMigration(closed) error = nil")
	}
}

func TestComisReportOutbox_RejectsUnboundAndInvalidAcknowledgements(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.Exec("UPDATE tasks SET managed_run_id = '' WHERE handle = ?", task.Handle); err != nil {
		t.Fatalf("corrupt exact binding: %v", err)
	}
	report := sqliteWorkerReport(task, "report-unbound", domain.ReportProgress)
	if _, err := store.CommitReport(context.Background(), directReportMutation(task, report, task.UpdatedAt.Add(time.Minute))); err == nil {
		t.Fatal("CommitReport(unbound outbox) error = nil")
	}
	if reports, err := store.ListAcceptedReports(context.Background(), task.Handle); err != nil || len(reports) != 0 {
		t.Fatalf("reports after atomic rejection = %#v, %v", reports, err)
	}
	if err := store.MarkComisReportDelivered(context.Background(), "bad operation", application.ComisReportAcknowledgement{}, time.Time{}); err == nil {
		t.Fatal("MarkComisReportDelivered(invalid) error = nil")
	}
}
