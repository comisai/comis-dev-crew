package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func TestReportAcceptance_ReplaysAcrossRestartAndRejectsAlteredPayload(t *testing.T) {
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, task := openReportFixture(t, databasePath)
	acceptedAt := time.Date(2026, time.August, 9, 15, 40, 0, 0, time.UTC)
	client := reportClient(t, store, task, acceptedAt)
	progress := sqliteWorkerReport(task, "report-progress", domain.ReportProgress)
	receipt, err := client.Report(context.Background(), progress)
	if err != nil {
		t.Fatalf("Report(progress) error = %v", err)
	}
	if receipt.StateVersion != 2 || receipt.AcceptedAt != acceptedAt {
		t.Fatalf("progress receipt = %#v, want version 2 and injected acceptance time", receipt)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restartedClient := reportClient(t, reopened, task, acceptedAt.Add(time.Hour))
	replay, err := restartedClient.Report(context.Background(), progress)
	if err != nil || replay != receipt {
		t.Fatalf("Report(replay) = %#v, %v, want original %#v", replay, err, receipt)
	}
	altered := progress
	altered.Summary = "altered progress must conflict"
	if _, err := restartedClient.Report(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("Report(altered replay) error = %v, want ErrConflict", err)
	}

	decision := sqliteWorkerReport(task, "report-decision", domain.ReportDecision)
	decision.ExternalKey = "decision-0001"
	if _, err := restartedClient.Report(context.Background(), decision); err != nil {
		t.Fatalf("Report(decision) error = %v", err)
	}
	resolution := sqliteWorkerReport(task, "report-resolution", domain.ReportResolution)
	resolution.ExternalKey = decision.ExternalKey
	if _, err := restartedClient.Report(context.Background(), resolution); err != nil {
		t.Fatalf("Report(resolution) error = %v", err)
	}
	storedTask, err := reopened.GetTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	if storedTask.State != domain.TaskWorking || storedTask.ReportCursor != 3 || storedTask.StateVersion != 4 {
		t.Fatalf("stored task = %#v, want working with cursor 3 at version 4", storedTask)
	}
	reports, err := reopened.ListAcceptedReports(context.Background(), task.Handle)
	if err != nil || len(reports) != 3 {
		t.Fatalf("ListAcceptedReports() = %#v, %v, want three durable reports", reports, err)
	}
	for index, accepted := range reports {
		if accepted.StateVersion != int64(index+2) || accepted.Report.LocalReportID == "" || accepted.SubjectDigest == "" {
			t.Fatalf("accepted report %d = %#v, want ordered exact durable evidence", index, accepted)
		}
	}
}

func TestReportAcceptance_ConcurrentIdenticalReportCreatesOneLogicalEffect(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	client := reportClient(t, store, task, time.Date(2026, time.August, 9, 15, 40, 0, 0, time.UTC))
	report := sqliteWorkerReport(task, "report-progress", domain.ReportProgress)
	start := make(chan struct{})
	receipts := make(chan domain.ReportReceipt, 2)
	errorsFound := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			receipt, err := client.Report(context.Background(), report)
			receipts <- receipt
			errorsFound <- err
		}()
	}
	close(start)
	first, second := <-receipts, <-receipts
	for range 2 {
		if err := <-errorsFound; err != nil {
			t.Fatalf("concurrent Report() error = %v", err)
		}
	}
	if first != second {
		t.Fatalf("concurrent receipts = %#v/%#v, want one logical outcome", first, second)
	}
	reports, err := store.ListAcceptedReports(context.Background(), task.Handle)
	if err != nil || len(reports) != 1 {
		t.Fatalf("ListAcceptedReports() = %#v, %v, want one report", reports, err)
	}
}

func openReportFixture(t *testing.T, databasePath string) (*Store, domain.Task) {
	t.Helper()
	store, err := Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	task := storeTask("task-report-0001", 1)
	task.ManagedRunID = "managed-run-0001"
	task.WorkspaceLeaseID = "workspace-lease-0001"
	task.State = domain.TaskLaunching
	if err := store.CreateTask(context.Background(), task); err != nil {
		_ = store.Close()
		t.Fatalf("CreateTask() error = %v", err)
	}
	return store, task
}

func reportClient(t *testing.T, store *Store, task domain.Task, acceptedAt time.Time) *reporter.Client {
	t.Helper()
	sink, err := application.NewReportSink(application.ReportSinkConfig{Store: store, Clock: func() time.Time { return acceptedAt }})
	if err != nil {
		t.Fatalf("NewReportSink() error = %v", err)
	}
	const credential = "fixture-credential-0000000000000001"
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: task.Handle, BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Credential: credential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	client, err := reporter.NewClient(endpoint, credential)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func sqliteWorkerReport(task domain.Task, localID string, kind domain.WorkerReportKind) domain.WorkerReport {
	observed := time.Date(2026, time.August, 9, 15, 39, 0, 0, time.UTC)
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: localID, BriefRevision: task.BriefRevision,
		BriefRevisionHash: task.BriefRevisionHash, Kind: kind, Summary: "bounded fixture report",
		WorkerObservedAt: &observed,
	}
}
