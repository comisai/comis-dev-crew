package reporter_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

const validCredential = "fixture-credential-0000000000000001"

func TestEndpoint_DerivesTaskAuthorityAndAcceptsPinnedSparseReport(t *testing.T) {
	acceptedAt := time.Date(2026, time.August, 9, 11, 5, 0, 0, time.UTC)
	sink := &recordingSink{receipt: domain.ReportReceipt{
		TaskHandle: "task-0001", LocalReportID: "report-0001", StateVersion: 7, AcceptedAt: acceptedAt,
	}}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	client, err := reporter.NewClient(endpoint, validCredential)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	report := sparseReport(3, strings.Repeat("a", 64))
	receipt, err := client.Report(context.Background(), report)
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	if sink.accepted.TaskHandle != "task-0001" || sink.accepted.Report != report {
		t.Fatalf("authenticated report = %#v, want endpoint-derived task and exact payload", sink.accepted)
	}
	if receipt != sink.receipt {
		t.Fatalf("receipt = %#v, want %#v", receipt, sink.receipt)
	}
}

func TestEndpoint_RejectsWrongCredentialAndStaleBriefWithoutCallingSink(t *testing.T) {
	sink := &recordingSink{}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	tests := []struct {
		name       string
		credential string
		report     domain.WorkerReport
		want       error
	}{
		{name: "wrong credential", credential: "wrong-credential-0000000000000000", report: sparseReport(3, strings.Repeat("a", 64)), want: reporter.ErrUnauthorized},
		{name: "stale revision", credential: validCredential, report: sparseReport(2, strings.Repeat("a", 64)), want: reporter.ErrStaleBrief},
		{name: "stale hash", credential: validCredential, report: sparseReport(3, strings.Repeat("b", 64)), want: reporter.ErrStaleBrief},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := reporter.NewClient(endpoint, test.credential)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			_, err = client.Report(context.Background(), test.report)
			if !errors.Is(err, test.want) {
				t.Fatalf("Report() error = %v, want %v", err, test.want)
			}
			if strings.Contains(err.Error(), test.credential) || strings.Contains(err.Error(), test.report.BriefRevisionHash) {
				t.Fatalf("safe report error leaked authority input: %q", err)
			}
		})
	}
	if sink.calls != 0 {
		t.Fatalf("sink calls = %d, want zero rejected reports", sink.calls)
	}
}

func TestEndpoint_RejectsInvalidPayloadAndMismatchedReceipt(t *testing.T) {
	sink := &recordingSink{receipt: domain.ReportReceipt{
		TaskHandle: "task-other", LocalReportID: "report-other", StateVersion: 1,
		AcceptedAt: time.Date(2026, time.August, 9, 11, 5, 0, 0, time.UTC),
	}}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	client, err := reporter.NewClient(endpoint, validCredential)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	invalid := sparseReport(3, strings.Repeat("a", 64))
	invalid.Kind = domain.WorkerReportKind("invented")
	if _, err := client.Report(context.Background(), invalid); !errors.Is(err, reporter.ErrInvalidReport) {
		t.Fatalf("Report(invalid) error = %v, want ErrInvalidReport", err)
	}
	if _, err := client.Report(context.Background(), sparseReport(3, strings.Repeat("a", 64))); !errors.Is(err, reporter.ErrInvalidReceipt) {
		t.Fatalf("Report(mismatched receipt) error = %v, want ErrInvalidReceipt", err)
	}
}

func TestEndpoint_ValidatesConfigurationContextAndSinkFailure(t *testing.T) {
	valid := reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: &recordingSink{},
	}
	tests := []struct {
		name   string
		mutate func(*reporter.EndpointConfig)
	}{
		{name: "task", mutate: func(config *reporter.EndpointConfig) { config.TaskHandle = "../escape" }},
		{name: "revision", mutate: func(config *reporter.EndpointConfig) { config.BriefRevision = 0 }},
		{name: "hash", mutate: func(config *reporter.EndpointConfig) { config.BriefRevisionHash = "bad" }},
		{name: "short credential", mutate: func(config *reporter.EndpointConfig) { config.Credential = "short" }},
		{name: "missing sink", mutate: func(config *reporter.EndpointConfig) { config.Sink = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := reporter.NewEndpoint(config); err == nil {
				t.Fatal("NewEndpoint() error = nil, want validation failure")
			}
		})
	}
	if _, err := reporter.NewClient(nil, validCredential); err == nil {
		t.Fatal("NewClient(nil endpoint) error = nil")
	}

	sinkFailure := errors.New("private sink cause")
	sink := &recordingSink{err: sinkFailure}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	client, err := reporter.NewClient(endpoint, validCredential)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Report(cancelled, sparseReport(3, strings.Repeat("a", 64))); !errors.Is(err, context.Canceled) {
		t.Fatalf("Report(cancelled) error = %v, want context.Canceled", err)
	}
	if _, err := client.Report(context.Background(), sparseReport(3, strings.Repeat("a", 64))); !errors.Is(err, sinkFailure) {
		t.Fatalf("Report(sink failure) error = %v, want preserved cause", err)
	}
}

type recordingSink struct {
	accepted domain.AuthenticatedReport
	receipt  domain.ReportReceipt
	err      error
	calls    int
}

func (sink *recordingSink) AcceptReport(_ context.Context, report domain.AuthenticatedReport) (domain.ReportReceipt, error) {
	sink.calls++
	sink.accepted = report
	return sink.receipt, sink.err
}

func sparseReport(revision int64, hash string) domain.WorkerReport {
	observed := time.Date(2026, time.August, 9, 11, 4, 0, 0, time.UTC)
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: "report-0001", BriefRevision: revision,
		BriefRevisionHash: hash, Kind: domain.ReportProgress, Summary: "fixture progress", WorkerObservedAt: &observed,
	}
}
