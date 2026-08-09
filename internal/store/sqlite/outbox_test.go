package sqlite

import (
	"context"
	"errors"
	"path/filepath"
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
	if err != nil || !found || replayedPending != pending {
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
	if err != nil || !found || restartedPending != pending {
		t.Fatalf("NextComisReport(restart) = %#v, %t, %v, want %#v", restartedPending, found, err, pending)
	}
	deliveredAt := acceptedAt.Add(time.Minute)
	ack := application.ComisReportAcknowledgement{
		ManagedRunID: pending.ManagedRunID, ServiceReportID: pending.ServiceReportID,
		AcceptedSequence: 11, RetainedUntil: deliveredAt.Add(24 * time.Hour),
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

func TestComisReportOutbox_RejectsUnboundAndInvalidAcknowledgements(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-unbound", 1)
	task.State = domain.TaskLaunching
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatal(err)
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
