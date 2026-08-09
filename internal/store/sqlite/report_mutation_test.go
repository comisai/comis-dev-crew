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

func TestReportAcceptance_RejectsInvalidMissingAndAmbiguousDecisionReports(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	acceptedAt := time.Date(2026, time.August, 9, 15, 40, 0, 0, time.UTC)
	progress := directReportMutation(task, sqliteWorkerReport(task, "report-progress", domain.ReportProgress), acceptedAt)

	invalid := progress
	invalid.SubjectDigest = "invalid"
	if _, err := store.CommitReport(ctx, invalid); err == nil {
		t.Fatal("CommitReport(invalid digest) error = nil")
	}
	missing := progress
	missing.Report.TaskHandle = "task-missing"
	missing.Report.Report.LocalReportID = "report-missing"
	if _, err := store.CommitReport(ctx, missing); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("CommitReport(missing task) error = %v, want ErrNotFound", err)
	}
	orphan := progress
	orphan.Report.Report = sqliteWorkerReport(task, "report-orphan-resolution", domain.ReportResolution)
	orphan.Report.Report.ExternalKey = "decision-orphan"
	if _, err := store.CommitReport(ctx, orphan); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitReport(orphan resolution) error = %v, want ErrConflict", err)
	}
	if _, err := store.CommitReport(ctx, progress); err != nil {
		t.Fatalf("CommitReport(progress) error = %v", err)
	}

	decisionReport := sqliteWorkerReport(task, "report-decision", domain.ReportDecision)
	decisionReport.ExternalKey = "decision-0001"
	decision := directReportMutation(task, decisionReport, acceptedAt)
	if _, err := store.CommitReport(ctx, decision); err != nil {
		t.Fatalf("CommitReport(decision) error = %v", err)
	}
	duplicateDecisionReport := decisionReport
	duplicateDecisionReport.LocalReportID = "report-decision-duplicate"
	duplicateDecision := directReportMutation(task, duplicateDecisionReport, acceptedAt)
	if _, err := store.CommitReport(ctx, duplicateDecision); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitReport(duplicate decision key) error = %v, want ErrConflict", err)
	}
	wrongResolutionReport := sqliteWorkerReport(task, "report-resolution-wrong", domain.ReportResolution)
	wrongResolutionReport.ExternalKey = "decision-wrong"
	wrongResolution := directReportMutation(task, wrongResolutionReport, acceptedAt)
	if _, err := store.CommitReport(ctx, wrongResolution); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitReport(wrong resolution key) error = %v, want ErrConflict", err)
	}
	resolutionReport := sqliteWorkerReport(task, "report-resolution", domain.ReportResolution)
	resolutionReport.ExternalKey = decisionReport.ExternalKey
	resolution := directReportMutation(task, resolutionReport, acceptedAt)
	if _, err := store.CommitReport(ctx, resolution); err != nil {
		t.Fatalf("CommitReport(resolution) error = %v", err)
	}
	duplicateResolutionReport := resolutionReport
	duplicateResolutionReport.LocalReportID = "report-resolution-duplicate"
	duplicateResolution := directReportMutation(task, duplicateResolutionReport, acceptedAt)
	if _, err := store.CommitReport(ctx, duplicateResolution); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitReport(duplicate resolution) error = %v, want ErrConflict", err)
	}

	nilObservation := sqliteWorkerReport(task, "report-no-observation", domain.ReportProgress)
	nilObservation.WorkerObservedAt = nil
	if _, err := store.CommitReport(ctx, directReportMutation(task, nilObservation, acceptedAt)); err != nil {
		t.Fatalf("CommitReport(no observation) error = %v", err)
	}
	reports, err := store.ListAcceptedReports(ctx, task.Handle)
	if err != nil || reports[len(reports)-1].Report.WorkerObservedAt != nil {
		t.Fatalf("ListAcceptedReports(no observation) = %#v, %v", reports, err)
	}
}

func TestReportAcceptance_RejectsIllegalStateExhaustionAndClosedStore(t *testing.T) {
	t.Run("illegal state", func(t *testing.T) {
		store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		t.Cleanup(func() { _ = store.Close() })
		task := storeTask("task-prepared", 1)
		if err := store.CreateTask(context.Background(), task); err != nil {
			t.Fatalf("CreateTask() error = %v", err)
		}
		mutation := directReportMutation(task, sqliteWorkerReport(task, "report-progress", domain.ReportProgress), task.UpdatedAt.Add(time.Minute))
		if _, err := store.CommitReport(context.Background(), mutation); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("CommitReport(illegal state) error = %v, want ErrInvalidTransition", err)
		}
	})

	t.Run("exhausted and closed", func(t *testing.T) {
		store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
		exhausted := storeOperation("op-exhaust-report", int64(^uint64(0)>>1))
		if err := store.RecordOperation(context.Background(), exhausted); err != nil {
			t.Fatalf("RecordOperation(exhausted) error = %v", err)
		}
		mutation := directReportMutation(task, sqliteWorkerReport(task, "report-progress", domain.ReportProgress), task.UpdatedAt.Add(time.Minute))
		if _, err := store.CommitReport(context.Background(), mutation); err == nil {
			t.Fatal("CommitReport(exhausted) error = nil")
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, err := store.CommitReport(context.Background(), mutation); err == nil {
			t.Fatal("CommitReport(closed) error = nil")
		}
		if _, err := store.ListAcceptedReports(context.Background(), task.Handle); err == nil {
			t.Fatal("ListAcceptedReports(closed) error = nil")
		}
		if _, err := store.ListAcceptedReports(context.Background(), "../escape"); err == nil {
			t.Fatal("ListAcceptedReports(invalid handle) error = nil")
		}
	})
}

func TestListAcceptedReports_RejectsCorruptEvidence(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  string
	}{
		{name: "observation time", column: "worker_observed_at", value: "not-a-time"},
		{name: "acceptance time", column: "accepted_at", value: "not-a-time"},
		{name: "report kind", column: "kind", value: "invented"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
			t.Cleanup(func() { _ = store.Close() })
			mutation := directReportMutation(task, sqliteWorkerReport(task, "report-progress", domain.ReportProgress), task.UpdatedAt.Add(time.Minute))
			if _, err := store.CommitReport(context.Background(), mutation); err != nil {
				t.Fatalf("CommitReport() error = %v", err)
			}
			query := "UPDATE reports SET " + test.column + " = ? WHERE task_handle = ?" // #nosec G202 -- column is a closed test fixture.
			if _, err := store.db.Exec(query, test.value, task.Handle); err != nil {
				t.Fatalf("corrupt report evidence: %v", err)
			}
			if _, err := store.ListAcceptedReports(context.Background(), task.Handle); err == nil {
				t.Fatal("ListAcceptedReports(corrupt) error = nil")
			}
			if _, err := store.CommitReport(context.Background(), mutation); err == nil {
				t.Fatal("CommitReport(corrupt replay) error = nil")
			}
		})
	}
}

func TestReportStorageHelpersRejectInvalidRecordsBeforeWriting(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	transaction, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	t.Cleanup(func() { _ = transaction.Rollback() })
	if err := insertAcceptedReport(context.Background(), transaction, domain.AcceptedReport{}); err == nil {
		t.Fatal("insertAcceptedReport(invalid) error = nil")
	}
	invalidTask := storeTask("task-invalid", 1)
	invalidTask.Handle = "../escape"
	if err := updateReportedTask(context.Background(), transaction, invalidTask); err == nil {
		t.Fatal("updateReportedTask(invalid) error = nil")
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

func directReportMutation(task domain.Task, report domain.WorkerReport, acceptedAt time.Time) application.ReportMutation {
	return application.ReportMutation{
		Report:        domain.AuthenticatedReport{TaskHandle: task.Handle, Report: report},
		SubjectDigest: strings.Repeat("a", 64), AcceptedAt: acceptedAt,
	}
}
