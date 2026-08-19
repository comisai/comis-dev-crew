package localapi

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func newDecisionClient(t *testing.T, caller CallerClass, queries ReadQueries) *Client {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{Queries: queries, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := filepath.Join(canonicalTempDir(t), "runtime", "devcrew.sock")
	server, err := Listen(socketPath, caller, handler)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server.Close() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Server.Serve() error = %v", err)
		}
	})
	client, err := NewClient(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

func decisionQueriesFixture() *apiQueries {
	aired := time.Unix(1_800_000_000, 0).UTC()
	next := aired.Add(30 * time.Minute)
	return &apiQueries{decisions: application.DecisionList{
		SchemaVersion: 1, CapturedAt: aired,
		Decisions: []application.TaskDecision{{
			TaskHandle: "task-decision-0001", ExternalKey: "schema-choice",
			Status: application.DecisionAwaitingHuman, Question: "which migration order applies",
			ReportedAt: aired, AskedAt: &aired, Airings: 1, LastAiringAt: &aired, NextAiringAt: &next,
		}},
	}}
}

// The operator console reads the open questions and one question's detail over
// the owner-only endpoint.
func TestClient_ReadsTheOpenDecisionInventory(t *testing.T) {
	client := newDecisionClient(t, CallerOperatorCLI, decisionQueriesFixture())

	list, err := client.ListDecisions(context.Background(), "read-decisions", ListDecisionsInput{})
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if len(list.Decisions) != 1 || list.Decisions[0].ExternalKey != "schema-choice" {
		t.Fatalf("decision list = %#v", list)
	}
	if list.Decisions[0].NextAiringAt == nil {
		t.Error("the inventory dropped the return schedule")
	}

	decision, err := client.ShowDecision(context.Background(), "read-decision", ShowDecisionInput{
		TaskHandle: "task-decision-0001", ExternalKey: "schema-choice",
	})
	if err != nil {
		t.Fatalf("ShowDecision() error = %v", err)
	}
	if decision.Question != "which migration order applies" {
		t.Fatalf("decision = %#v", decision)
	}
}

// Private task detail is an operator surface. §20.3 makes observation and custody
// deliberately asymmetric with the model surface, so the authority boundary lives
// in the handler rather than only in which tools the facade happens to expose —
// a facade that grew a tool must not thereby gain the capability.
func TestHandler_RefusesDecisionReadsFromTheModelFacade(t *testing.T) {
	client := newDecisionClient(t, CallerMCPFacade, decisionQueriesFixture())

	if _, err := client.ListDecisions(context.Background(), "read-decisions", ListDecisionsInput{}); err == nil {
		t.Error("ListDecisions(MCP facade) error = nil, want a refusal")
	}
	if _, err := client.ShowDecision(context.Background(), "read-decision", ShowDecisionInput{
		TaskHandle: "task-decision-0001", ExternalKey: "schema-choice",
	}); err == nil {
		t.Error("ShowDecision(MCP facade) error = nil, want a refusal")
	}
}

// Both reads are classified as reads, so no adapter can treat inspecting a
// question as a state change.
func TestDecisionMethods_AreValidReads(t *testing.T) {
	for _, method := range []Method{MethodListDecisions, MethodShowDecision} {
		if !method.valid() {
			t.Errorf("%s is not a valid method", method)
		}
		if method.SideEffect() != SideEffectRead {
			t.Errorf("%s side effect = %q, want read", method, method.SideEffect())
		}
		if methodAllowed(CallerMCPFacade, method) {
			t.Errorf("%s is reachable from the model facade", method)
		}
		if !methodAllowed(CallerOperatorCLI, method) {
			t.Errorf("%s is unreachable from the operator console", method)
		}
		for _, caller := range []CallerClass{CallerWorkerReport, CallerComisControl} {
			if methodAllowed(caller, method) {
				t.Errorf("%s is reachable from %s", method, caller)
			}
		}
	}
}
