package reporter_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

type recordingAuditor struct {
	tasks []string
	err   error
}

func (auditor *recordingAuditor) RecordReportAuthenticationFailure(_ context.Context, taskHandle string) error {
	auditor.tasks = append(auditor.tasks, taskHandle)
	return auditor.err
}

func auditedEndpoint(t *testing.T, auditor reporter.AuthenticationAuditor, sink reporter.ReportSink) *reporter.Endpoint {
	t.Helper()
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: sink, Auditor: auditor,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	return endpoint
}

// TestEndpoint_AuditsARejectedCredential records the one event on this boundary
// that means somebody presented authority they do not hold.
//
// A rejected credential advances no task and writes no report, so every durable
// surface the service keeps is silent about it. The threat this endpoint exists
// to stop — one worker reporting as another task — would therefore succeed or
// fail with equally no trace, and an operator asking afterwards whether it was
// ever attempted has nothing to read.
func TestEndpoint_AuditsARejectedCredential(t *testing.T) {
	auditor := &recordingAuditor{}
	sink := &recordingSink{}
	endpoint := auditedEndpoint(t, auditor, sink)

	client, err := reporter.NewClient(endpoint, "wrong-credential-0000000000000000")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Report(context.Background(), sparseReport(3, strings.Repeat("a", 64))); !errors.Is(err, reporter.ErrUnauthorized) {
		t.Fatalf("Report() error = %v, want ErrUnauthorized", err)
	}
	if len(auditor.tasks) != 1 || auditor.tasks[0] != "task-0001" {
		t.Fatalf("audited failures = %v, want exactly one for task-0001", auditor.tasks)
	}
	if sink.calls != 0 {
		t.Errorf("sink calls = %d, want zero for a rejected credential", sink.calls)
	}
}

// TestEndpoint_AuditsOnlyAuthenticationFailures keeps the trail meaningful. A
// stale brief or a malformed body is a correctly credentialed worker getting
// something wrong; recording those as authentication failures would bury the
// one event that means an identity boundary was tested.
func TestEndpoint_AuditsOnlyAuthenticationFailures(t *testing.T) {
	auditor := &recordingAuditor{}
	sink := &recordingSink{receipt: domain.ReportReceipt{
		TaskHandle: "task-0001", LocalReportID: "report-0001", StateVersion: 7,
		AcceptedAt: time.Date(2026, time.August, 9, 11, 5, 0, 0, time.UTC),
	}}
	endpoint := auditedEndpoint(t, auditor, sink)

	client, err := reporter.NewClient(endpoint, validCredential)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Report(context.Background(), sparseReport(2, strings.Repeat("a", 64))); !errors.Is(err, reporter.ErrStaleBrief) {
		t.Fatalf("Report(stale brief) error = %v, want ErrStaleBrief", err)
	}
	if _, err := client.Report(context.Background(), sparseReport(3, strings.Repeat("a", 64))); err != nil {
		t.Fatalf("Report(valid) error = %v", err)
	}
	if len(auditor.tasks) != 0 {
		t.Fatalf("audited failures = %v, want none", auditor.tasks)
	}
}

// TestEndpoint_RefusesAnEndpointThatCannotAudit fails closed. An endpoint with
// no way to record a rejection is an authentication boundary nobody can review,
// which is the gap this record exists to close.
func TestEndpoint_RefusesAnEndpointThatCannotAudit(t *testing.T) {
	if endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: validCredential, Sink: &recordingSink{},
	}); err == nil {
		t.Fatalf("NewEndpoint() without an auditor = %#v, want a refusal", endpoint)
	}
}

// TestEndpoint_StillRejectsWhenTheAuditWriteFails keeps the refusal primary. A
// trail that cannot be written is worth reporting, and it is never a reason to
// let an unauthorized report through.
func TestEndpoint_StillRejectsWhenTheAuditWriteFails(t *testing.T) {
	auditor := &recordingAuditor{err: errors.New("durable trail unavailable")}
	endpoint := auditedEndpoint(t, auditor, &recordingSink{})
	client, err := reporter.NewClient(endpoint, "wrong-credential-0000000000000000")
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	_, err = client.Report(context.Background(), sparseReport(3, strings.Repeat("a", 64)))
	if !errors.Is(err, reporter.ErrUnauthorized) {
		t.Fatalf("Report() error = %v, want the rejection preserved", err)
	}
}

type recordingBoundaryLogger struct {
	records []application.BoundaryRecord
}

func (logger *recordingBoundaryLogger) Record(record application.BoundaryRecord) {
	logger.records = append(logger.records, record)
}

// TestEndpoint_RecordsEveryWorkerCrossing gives an operator the only view there
// is of a confined worker talking. Each closed refusal carries its own kind, so
// a bad credential and a stale brief are distinguishable without a debugger.
func TestEndpoint_RecordsEveryWorkerCrossing(t *testing.T) {
	accepted := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	for name, test := range map[string]struct {
		credential string
		report     domain.WorkerReport
		outcome    application.BoundaryOutcome
		kind       domain.ErrorCode
	}{
		"accepted": {
			credential: validCredential, report: sparseReport(3, strings.Repeat("a", 64)),
			outcome: application.BoundaryCompleted,
		},
		"rejected credential": {
			credential: "wrong-credential-0000000000000000", report: sparseReport(3, strings.Repeat("a", 64)),
			outcome: application.BoundaryFailed, kind: domain.ErrorUnauthorized,
		},
		"stale brief": {
			credential: validCredential, report: sparseReport(2, strings.Repeat("a", 64)),
			outcome: application.BoundaryFailed, kind: domain.ErrorConflict,
		},
	} {
		t.Run(name, func(t *testing.T) {
			logger := &recordingBoundaryLogger{}
			endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
				TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
				Credential: validCredential, Auditor: &recordingAuditor{},
				Sink: &recordingSink{receipt: domain.ReportReceipt{
					TaskHandle: "task-0001", LocalReportID: "report-0001", StateVersion: 7, AcceptedAt: accepted,
				}},
				Logger: logger, Clock: func() time.Time { return accepted },
			})
			if err != nil {
				t.Fatalf("NewEndpoint() error = %v", err)
			}
			client, err := reporter.NewClient(endpoint, test.credential)
			if err != nil {
				t.Fatalf("NewClient() error = %v", err)
			}
			_, _ = client.Report(context.Background(), test.report)
			if len(logger.records) != 1 {
				t.Fatalf("recorded %d crossings, want exactly 1", len(logger.records))
			}
			record := logger.records[0]
			if record.Boundary != application.BoundaryReporter || record.TaskHandle != "task-0001" {
				t.Errorf("record identity = %#v", record)
			}
			if record.Outcome != test.outcome {
				t.Errorf("outcome = %q, want %q", record.Outcome, test.outcome)
			}
			if record.ErrorKind != test.kind {
				t.Errorf("error kind = %q, want %q", record.ErrorKind, test.kind)
			}
			if test.outcome == application.BoundaryFailed && record.Hint == "" {
				t.Error("a failed crossing carried no hint")
			}
		})
	}
}
