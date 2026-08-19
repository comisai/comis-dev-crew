package comiswire

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type decisionRaiserSender struct {
	mu       sync.Mutex
	requests []ReportRequestParams
	failures []error
	result   func(ReportRequestParams) ReportResponseResult
}

func (sender *decisionRaiserSender) Report(
	_ context.Context,
	request ReportRequestParams,
) (ReportResponseResult, error) {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.requests = append(sender.requests, request)
	if len(sender.failures) != 0 {
		failure := sender.failures[0]
		sender.failures = sender.failures[1:]
		if failure != nil {
			return ReportResponseResult{}, failure
		}
	}
	if sender.result != nil {
		return sender.result(request), nil
	}
	return ReportResponseResult{ManagedRunID: request.ManagedRunID, ServiceReportID: request.ServiceReportID}, nil
}

func decisionRaiserFixture(t *testing.T, sender ReportSender) *DecisionRaiser {
	t.Helper()
	raiser, err := NewDecisionRaiser(DecisionRaiserConfig{Sender: sender})
	if err != nil {
		t.Fatalf("NewDecisionRaiser() error = %v", err)
	}
	return raiser
}

func openDecisionFixture() application.OpenDecision {
	return application.OpenDecision{
		TaskHandle: "task-decision-0001", ManagedRunID: "managed-run-0001",
		ExternalKey: "schema-choice", Summary: "which migration order applies",
		Details: "the two candidate orders differ in their backfill window",
	}
}

// Re-surfacing uses the same generic attention path the question took the first
// time. It is not a new kind of message, so the host reduces it exactly as it
// reduced the original: one open question, asked again.
func TestDecisionRaiser_RaisesTheQuestionOnTheGenericAttentionPath(t *testing.T) {
	sender := &decisionRaiserSender{}
	raiser := decisionRaiserFixture(t, sender)

	if err := raiser.RaiseOpenDecision(context.Background(), openDecisionFixture()); err != nil {
		t.Fatalf("RaiseOpenDecision() error = %v", err)
	}
	if len(sender.requests) != 1 {
		t.Fatalf("report calls = %d, want one", len(sender.requests))
	}
	request := sender.requests[0]
	if request.Kind != ReportKindAttention {
		t.Errorf("kind = %q, want %q", request.Kind, ReportKindAttention)
	}
	if string(request.ManagedRunID) != "managed-run-0001" {
		t.Errorf("managed run = %q", request.ManagedRunID)
	}
	if request.ExternalKey == nil || *request.ExternalKey != "schema-choice" {
		t.Errorf("external key = %v, want the decision key", request.ExternalKey)
	}
	if request.Summary != "which migration order applies" {
		t.Errorf("summary = %q, want the original question", request.Summary)
	}
	if request.Details == nil || *request.Details != "the two candidate orders differ in their backfill window" {
		t.Errorf("details = %v, want the original question body", request.Details)
	}
	if request.ObservedAtMs != nil {
		t.Error("a service re-surfacing carries no worker observation time")
	}
	if len(request.ArtifactRefs) != 0 {
		t.Error("an attention re-surfacing carries no evidence references")
	}
}

// An uncertain send leaves the host outcome unknown, and the ledger records
// nothing until it succeeds. The retry therefore has to reuse the identity, so
// the host recognizes the repeat instead of asking the liaison twice.
func TestDecisionRaiser_ReusesOneIdentityUntilTheSurfacingIsRecorded(t *testing.T) {
	sender := &decisionRaiserSender{failures: []error{errors.New("uncertain send")}}
	raiser := decisionRaiserFixture(t, sender)
	decision := openDecisionFixture()

	if err := raiser.RaiseOpenDecision(context.Background(), decision); err == nil {
		t.Fatal("RaiseOpenDecision(uncertain) error = nil")
	}
	if err := raiser.RaiseOpenDecision(context.Background(), decision); err != nil {
		t.Fatalf("RaiseOpenDecision(retry) error = %v", err)
	}
	if len(sender.requests) != 2 {
		t.Fatalf("report calls = %d, want two", len(sender.requests))
	}
	if sender.requests[0].OperationID != sender.requests[1].OperationID ||
		sender.requests[0].ServiceReportID != sender.requests[1].ServiceReportID {
		t.Fatalf("retry minted a new identity: %#v / %#v", sender.requests[0], sender.requests[1])
	}

	recorded := decision
	recorded.SurfaceCount = 1
	if err := raiser.RaiseOpenDecision(context.Background(), recorded); err != nil {
		t.Fatalf("RaiseOpenDecision(next surfacing) error = %v", err)
	}
	if sender.requests[2].OperationID == sender.requests[1].OperationID ||
		sender.requests[2].ServiceReportID == sender.requests[1].ServiceReportID {
		t.Fatalf("the next surfacing replayed a recorded identity: %#v", sender.requests[2])
	}
}

// An acknowledgement for some other run or report proves nothing about this
// question. Accepting it would record a surfacing that never reached anybody
// and then hold the decision silent for a full interval.
func TestDecisionRaiser_RefusesAnAcknowledgementForSomethingElse(t *testing.T) {
	for name, result := range map[string]func(ReportRequestParams) ReportResponseResult{
		"other run": func(request ReportRequestParams) ReportResponseResult {
			return ReportResponseResult{ManagedRunID: "managed-run-0002", ServiceReportID: request.ServiceReportID}
		},
		"other report": func(request ReportRequestParams) ReportResponseResult {
			return ReportResponseResult{ManagedRunID: request.ManagedRunID, ServiceReportID: "service-attention-elsewhere"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			sender := &decisionRaiserSender{result: result}
			raiser := decisionRaiserFixture(t, sender)
			err := raiser.RaiseOpenDecision(context.Background(), openDecisionFixture())
			if err == nil {
				t.Fatal("RaiseOpenDecision() error = nil, want an identity refusal")
			}
			if !strings.Contains(err.Error(), "identity") {
				t.Fatalf("RaiseOpenDecision() error = %v, want it to name the identity mismatch", err)
			}
		})
	}
}

// A decision the host could never accept is refused here rather than sent and
// rejected, so an unraisable row cannot consume the cadence.
func TestDecisionRaiser_RefusesAnUnraisableDecisionWithoutSending(t *testing.T) {
	oversize := openDecisionFixture()
	oversize.Details = strings.Repeat("d", MaxReportBytes)
	cases := map[string]application.OpenDecision{
		"no task":           {ManagedRunID: "managed-run-0001", ExternalKey: "schema-choice", Summary: "question"},
		"no run":            {TaskHandle: "task-decision-0001", ExternalKey: "schema-choice", Summary: "question"},
		"no key":            {TaskHandle: "task-decision-0001", ManagedRunID: "managed-run-0001", Summary: "question"},
		"no question":       {TaskHandle: "task-decision-0001", ManagedRunID: "managed-run-0001", ExternalKey: "schema-choice"},
		"negative count":    {TaskHandle: "task-decision-0001", ManagedRunID: "managed-run-0001", ExternalKey: "schema-choice", Summary: "question", SurfaceCount: -1},
		"oversize question": oversize,
	}
	for name, decision := range cases {
		t.Run(name, func(t *testing.T) {
			sender := &decisionRaiserSender{}
			raiser := decisionRaiserFixture(t, sender)
			if err := raiser.RaiseOpenDecision(context.Background(), decision); err == nil {
				t.Fatal("RaiseOpenDecision() error = nil, want a refusal")
			}
			if len(sender.requests) != 0 {
				t.Fatalf("an unraisable decision reached the host: %#v", sender.requests)
			}
		})
	}
}

func TestNewDecisionRaiser_RequiresASenderAndACaller(t *testing.T) {
	if _, err := NewDecisionRaiser(DecisionRaiserConfig{}); err == nil {
		t.Error("NewDecisionRaiser(no sender) error = nil")
	}
	raiser := decisionRaiserFixture(t, &decisionRaiserSender{})
	if err := raiser.RaiseOpenDecision(missingRaiserContext(), openDecisionFixture()); err == nil {
		t.Error("RaiseOpenDecision(no context) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := raiser.RaiseOpenDecision(canceled, openDecisionFixture()); err == nil {
		t.Error("RaiseOpenDecision(canceled) error = nil")
	}
}

func missingRaiserContext() context.Context { return nil }
