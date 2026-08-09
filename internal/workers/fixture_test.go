package workers_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func TestFixture_AcceptsPinnedBriefReportsProgressRequestsOneDecisionAndResolves(t *testing.T) {
	harness := newFixtureHarness(t, workers.FaultNone, fixtureCredential)
	worker, err := workers.NewFixture(harness.config())
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	if err := worker.Run(context.Background()); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if harness.decisions.calls != 1 || harness.decisions.key != "decision-0001" {
		t.Fatalf("decision source calls/key = %d/%q, want one exact request", harness.decisions.calls, harness.decisions.key)
	}
	if len(harness.sink.reports) != 4 {
		t.Fatalf("accepted reports = %d, want progress, decision, resolution, candidate", len(harness.sink.reports))
	}
	wantKinds := []domain.WorkerReportKind{domain.ReportProgress, domain.ReportDecision, domain.ReportResolution, domain.ReportCandidateComplete}
	wantIDs := []string{"fixture-progress", "fixture-decision", "fixture-resolution", "fixture-candidate"}
	for index, accepted := range harness.sink.reports {
		if accepted.TaskHandle != "task-0001" || accepted.Report.Kind != wantKinds[index] || accepted.Report.LocalReportID != wantIDs[index] {
			t.Fatalf("report %d = %#v, want endpoint-derived task, kind %q, ID %q", index, accepted, wantKinds[index], wantIDs[index])
		}
		if accepted.Report.BriefRevision != harness.brief.Revision || accepted.Report.BriefRevisionHash != harness.brief.RevisionHash {
			t.Fatalf("report %d did not preserve exact brief pin: %#v", index, accepted.Report)
		}
	}
	if harness.sink.reports[1].Report.ExternalKey != "decision-0001" || harness.sink.reports[2].Report.ExternalKey != "decision-0001" {
		t.Fatal("decision and resolution must use the same bounded key")
	}
}

func TestFixture_StopsAtControlledFaultBoundaries(t *testing.T) {
	tests := []struct {
		fault         workers.FaultPoint
		wantReports   int
		wantDecisions int
	}{
		{fault: workers.FaultBeforeProgress, wantReports: 0, wantDecisions: 0},
		{fault: workers.FaultAfterProgress, wantReports: 1, wantDecisions: 0},
		{fault: workers.FaultAfterDecision, wantReports: 2, wantDecisions: 0},
		{fault: workers.FaultAfterResolution, wantReports: 3, wantDecisions: 1},
		{fault: workers.FaultAfterCandidate, wantReports: 4, wantDecisions: 1},
	}
	for _, test := range tests {
		t.Run(string(test.fault), func(t *testing.T) {
			harness := newFixtureHarness(t, test.fault, fixtureCredential)
			worker, err := workers.NewFixture(harness.config())
			if err != nil {
				t.Fatalf("NewFixture() error = %v", err)
			}
			err = worker.Run(context.Background())
			if !errors.Is(err, workers.ErrInjectedFault) {
				t.Fatalf("Run() error = %v, want ErrInjectedFault", err)
			}
			if len(harness.sink.reports) != test.wantReports || harness.decisions.calls != test.wantDecisions {
				t.Fatalf("reports/decisions = %d/%d, want %d/%d", len(harness.sink.reports), harness.decisions.calls, test.wantReports, test.wantDecisions)
			}
		})
	}
}

func TestFixture_FailsClosedOnWrongReporterCredentialStaleBriefAndDecisionFailure(t *testing.T) {
	t.Run("wrong credential", func(t *testing.T) {
		harness := newFixtureHarness(t, workers.FaultNone, "wrong-credential-0000000000000000")
		worker, err := workers.NewFixture(harness.config())
		if err != nil {
			t.Fatalf("NewFixture() error = %v", err)
		}
		err = worker.Run(context.Background())
		if !errors.Is(err, reporter.ErrUnauthorized) {
			t.Fatalf("Run() error = %v, want ErrUnauthorized", err)
		}
		if len(harness.sink.reports) != 0 {
			t.Fatalf("accepted reports = %d, want zero", len(harness.sink.reports))
		}
	})

	t.Run("altered brief", func(t *testing.T) {
		harness := newFixtureHarness(t, workers.FaultNone, fixtureCredential)
		config := harness.config()
		config.Brief.Content += "altered"
		if _, err := workers.NewFixture(config); err == nil {
			t.Fatal("NewFixture(altered brief) error = nil")
		}
	})

	t.Run("decision source failure", func(t *testing.T) {
		harness := newFixtureHarness(t, workers.FaultNone, fixtureCredential)
		privateCause := errors.New("private decision source detail")
		harness.decisions.err = privateCause
		worker, err := workers.NewFixture(harness.config())
		if err != nil {
			t.Fatalf("NewFixture() error = %v", err)
		}
		err = worker.Run(context.Background())
		if !errors.Is(err, privateCause) || strings.Contains(err.Error(), privateCause.Error()) {
			t.Fatalf("Run() error = %q, want safe wrapper preserving cause", err)
		}
		if len(harness.sink.reports) != 2 || harness.decisions.calls != 1 {
			t.Fatalf("reports/decisions = %d/%d, want decision durably emitted before failure", len(harness.sink.reports), harness.decisions.calls)
		}
	})
}

func TestFixture_ValidatesDependenciesClosedFaultsAndCancellation(t *testing.T) {
	harness := newFixtureHarness(t, workers.FaultNone, fixtureCredential)
	valid := harness.config()
	tests := []struct {
		name   string
		mutate func(*workers.FixtureConfig)
	}{
		{name: "missing reporter", mutate: func(config *workers.FixtureConfig) { config.Reporter = nil }},
		{name: "missing decisions", mutate: func(config *workers.FixtureConfig) { config.Decisions = nil }},
		{name: "missing clock", mutate: func(config *workers.FixtureConfig) { config.Clock = nil }},
		{name: "invalid report prefix", mutate: func(config *workers.FixtureConfig) { config.ReportIDPrefix = "../escape" }},
		{name: "invalid decision key", mutate: func(config *workers.FixtureConfig) { config.DecisionKey = "bad key" }},
		{name: "unknown fault", mutate: func(config *workers.FixtureConfig) { config.Fault = workers.FaultPoint("invented") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := workers.NewFixture(config); err == nil {
				t.Fatal("NewFixture() error = nil, want configuration rejection")
			}
		})
	}

	worker, err := workers.NewFixture(valid)
	if err != nil {
		t.Fatalf("NewFixture() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := worker.Run(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v, want context.Canceled", err)
	}
	if len(harness.sink.reports) != 0 {
		t.Fatalf("accepted reports = %d, want zero after pre-cancel", len(harness.sink.reports))
	}
	//lint:ignore SA1012 The boundary test proves nil cannot reach a reporter or decision seam.
	if err := worker.Run(nil); err == nil {
		t.Fatal("Run(nil context) error = nil")
	}
}

const fixtureCredential = "fixture-credential-0000000000000001"

type fixtureHarness struct {
	brief     domain.WorkerBrief
	reporter  *reporter.Client
	sink      *fixtureSink
	decisions *fixtureDecisions
	clock     time.Time
	fault     workers.FaultPoint
}

func newFixtureHarness(t *testing.T, fault workers.FaultPoint, clientCredential string) *fixtureHarness {
	t.Helper()
	brief := fixtureBrief()
	clock := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	sink := &fixtureSink{acceptedAt: clock}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
		Credential: fixtureCredential, Sink: sink,
	})
	if err != nil {
		t.Fatalf("NewEndpoint() error = %v", err)
	}
	client, err := reporter.NewClient(endpoint, clientCredential)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return &fixtureHarness{
		brief: brief, reporter: client, sink: sink,
		decisions: &fixtureDecisions{answer: "use the bounded option"}, clock: clock, fault: fault,
	}
}

func (harness *fixtureHarness) config() workers.FixtureConfig {
	return workers.FixtureConfig{
		Brief: harness.brief, Reporter: harness.reporter, Decisions: harness.decisions,
		Clock: func() time.Time { return harness.clock }, ReportIDPrefix: "fixture",
		DecisionKey: "decision-0001", Fault: harness.fault,
	}
}

type fixtureSink struct {
	reports    []domain.AuthenticatedReport
	acceptedAt time.Time
}

func (sink *fixtureSink) AcceptReport(_ context.Context, report domain.AuthenticatedReport) (domain.ReportReceipt, error) {
	sink.reports = append(sink.reports, report)
	return domain.ReportReceipt{
		TaskHandle: report.TaskHandle, LocalReportID: report.Report.LocalReportID,
		StateVersion: int64(len(sink.reports)), AcceptedAt: sink.acceptedAt,
	}, nil
}

type fixtureDecisions struct {
	answer string
	err    error
	key    string
	calls  int
}

func (decisions *fixtureDecisions) AwaitDecision(_ context.Context, key string) (string, error) {
	decisions.calls++
	decisions.key = key
	return decisions.answer, decisions.err
}

func fixtureBrief() domain.WorkerBrief {
	content := "taskHandle: task-0001\nbriefRevision: 1\nfixture: deterministic\n"
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	return domain.WorkerBrief{Revision: 1, RevisionHash: digest, Content: content}
}
