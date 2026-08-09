package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestReportSinkBuildsCanonicalDigestAndInjectedAcceptanceTime(t *testing.T) {
	acceptedAt := time.Date(2026, time.August, 9, 15, 30, 0, 0, time.UTC)
	store := &reportMutationStore{receipt: domain.ReportReceipt{
		TaskHandle: "task-0001", LocalReportID: "report-0001", StateVersion: 8, AcceptedAt: acceptedAt,
	}}
	sink, err := NewReportSink(ReportSinkConfig{Store: store, Clock: func() time.Time { return acceptedAt }})
	if err != nil {
		t.Fatalf("NewReportSink() error = %v", err)
	}
	report := authenticatedApplicationReport()
	receipt, err := sink.AcceptReport(context.Background(), report)
	if err != nil {
		t.Fatalf("AcceptReport() error = %v", err)
	}
	if receipt != store.receipt || store.mutation.Report != report || len(store.mutation.SubjectDigest) != 64 || store.mutation.AcceptedAt != acceptedAt {
		t.Fatalf("report mutation/receipt = %#v/%#v, want exact report, SHA-256 subject, and injected time", store.mutation, receipt)
	}
}

func TestReportSinkRejectsInvalidDependenciesContextAndPayloadBeforeCommit(t *testing.T) {
	valid := ReportSinkConfig{Store: &reportMutationStore{}, Clock: time.Now}
	if _, err := NewReportSink(ReportSinkConfig{}); err == nil {
		t.Fatal("NewReportSink(empty) error = nil")
	}
	sink, err := NewReportSink(valid)
	if err != nil {
		t.Fatalf("NewReportSink() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sink.AcceptReport(cancelled, authenticatedApplicationReport()); !errors.Is(err, context.Canceled) {
		t.Fatalf("AcceptReport(cancelled) error = %v, want context.Canceled", err)
	}
	invalid := authenticatedApplicationReport()
	invalid.TaskHandle = "../escape"
	if _, err := sink.AcceptReport(context.Background(), invalid); err == nil {
		t.Fatal("AcceptReport(invalid) error = nil")
	}
	//lint:ignore SA1012 The application boundary must reject nil before store access.
	if _, err := sink.AcceptReport(nil, authenticatedApplicationReport()); err == nil {
		t.Fatal("AcceptReport(nil) error = nil")
	}
}

type reportMutationStore struct {
	mutation ReportMutation
	receipt  domain.ReportReceipt
}

func (store *reportMutationStore) CommitReport(_ context.Context, mutation ReportMutation) (domain.ReportReceipt, error) {
	store.mutation = mutation
	return store.receipt, nil
}

func authenticatedApplicationReport() domain.AuthenticatedReport {
	observed := time.Date(2026, time.August, 9, 15, 29, 0, 0, time.UTC)
	return domain.AuthenticatedReport{TaskHandle: "task-0001", Report: domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: "report-0001", BriefRevision: 1,
		BriefRevisionHash: strings.Repeat("a", 64), Kind: domain.ReportProgress,
		Summary: "bounded progress", WorkerObservedAt: &observed,
	}}
}
