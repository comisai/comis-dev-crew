package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func decisionListFixture() application.DecisionList {
	aired := time.Date(2026, time.August, 19, 9, 0, 0, 0, time.UTC)
	next := aired.Add(30 * time.Minute)
	reported := aired.Add(-time.Minute)
	return application.DecisionList{
		SchemaVersion: 1, StateVersion: 7, CapturedAt: aired,
		Decisions: []application.TaskDecision{
			{
				StateVersion: 7, TaskHandle: "task-0001", ExternalKey: "schema-choice",
				Status: application.DecisionAwaitingHuman, Question: "which migration order applies",
				Detail:     "the two candidate orders differ in their backfill window",
				ReportedAt: reported, AskedAt: &aired, Airings: 2,
				LastAiringAt: &aired, NextAiringAt: &next,
			},
			{
				StateVersion: 7, TaskHandle: "task-0002", ExternalKey: "rollout-window",
				Status: application.DecisionAwaitingHost, Question: "which rollout window is acceptable",
				ReportedAt: reported,
			},
		},
	}
}

// An operator asking "what is waiting on me" needs the posture and the return
// time in the listing itself. A row saying only that a question is open would
// force a second command to learn whether anybody has even been asked yet.
func TestCLI_TheDecisionListingNamesThePostureAndTheReturnTime(t *testing.T) {
	client := &fakeClient{decisions: decisionListFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"decisions", "list"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(decisions list) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{
		"task-0001", "schema-choice", "awaiting_human", "task-0002", "rollout-window", "awaiting_host",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("listing omitted %q: %s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "2026-08-19T09:30:00Z") {
		t.Errorf("listing omitted the next return time: %s", rendered)
	}
	if len(client.calls) != 1 || client.calls[0] != "decisions:" {
		t.Errorf("client calls = %v, want one fleet-wide inventory read", client.calls)
	}
}

// A question nobody has been asked yet has no return time. Rendering a zero
// instant would read as long overdue, which is the opposite of the truth.
func TestCLI_AnUnaskedDecisionShowsNoReturnTime(t *testing.T) {
	client := &fakeClient{decisions: decisionListFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"decisions", "list"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(decisions list) = %d: %s", code, output.String())
	}
	if strings.Contains(output.String(), "0001-01-01") {
		t.Errorf("an absent return time rendered as a zero instant: %s", output.String())
	}
}

// Scoping is a service-side read, not a client-side filter: naming one task must
// not send another task's private questions across the socket.
func TestCLI_TheDecisionListingScopesToOneTaskAtTheService(t *testing.T) {
	client := &fakeClient{decisions: decisionListFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"decisions", "list", "--task", "task-0001"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(decisions list --task) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "decisions:task-0001" {
		t.Errorf("client calls = %v, want the scoped inventory read", client.calls)
	}
}

// A decision is named by its task and its key: E0 has no separate decision
// identity, so the CLI names the two facts that do identify it.
func TestCLI_ShowingOneDecisionCarriesTheQuestionBody(t *testing.T) {
	client := &fakeClient{decision: decisionListFixture().Decisions[0]}
	var output bytes.Buffer

	code := Run(context.Background(), []string{"decision", "show", "task-0001", "schema-choice"}, &output, &output, testConfig(client))
	if code != 0 {
		t.Fatalf("Run(decision show) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{"schema-choice", "which migration order applies", "backfill window"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("detail omitted %q: %s", want, rendered)
		}
	}
	if len(client.calls) != 1 || client.calls[0] != "decision:task-0001:schema-choice" {
		t.Errorf("client calls = %v, want one scoped decision read", client.calls)
	}
}

// Malformed invocations are refused before any socket work, so a typo cannot
// become a fleet-wide read or a call with an empty reference.
func TestCLI_RefusesMalformedDecisionInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"decisions extra argument": {"decisions", "list", "task-0001"},
		"decisions unknown verb":   {"decisions", "show"},
		"decisions bare flag":      {"decisions", "list", "--task"},
		"decisions bad handle":     {"decisions", "list", "--task", "not a handle"},
		"decision no key":          {"decision", "show", "task-0001"},
		"decision unknown verb":    {"decision", "list", "task-0001", "schema-choice"},
		"decision bad key":         {"decision", "show", "task-0001", "not a key"},
		"decision bad format":      {"decision", "show", "task-0001", "schema-choice", "--format", "yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{decisions: decisionListFixture()}
			var output bytes.Buffer
			if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != ExitUsage {
				t.Fatalf("Run(%v) = %d, want %d: %s", args, code, ExitUsage, output.String())
			}
			if len(client.calls) != 0 {
				t.Fatalf("a malformed invocation reached the service: %v", client.calls)
			}
		})
	}
}

// JSON is the machine surface for both reads and stays a stable projection.
func TestCLI_TheDecisionReadsOfferAStableJSONProjection(t *testing.T) {
	client := &fakeClient{decisions: decisionListFixture(), decision: decisionListFixture().Decisions[0]}
	for _, args := range [][]string{
		{"decisions", "list", "--format", "json"},
		{"decision", "show", "task-0001", "schema-choice", "--format", "json"},
	} {
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != 0 {
			t.Fatalf("Run(%v) = %d: %s", args, code, output.String())
		}
		if !strings.Contains(output.String(), "\"externalKey\": \"schema-choice\"") {
			t.Errorf("JSON output = %s", output.String())
		}
	}
}

// The help text is where an operator learns the commands exist.
func TestCLI_DocumentsTheDecisionCommands(t *testing.T) {
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &output, &output, Config{}); code != 0 {
		t.Fatalf("Run(--help) = %d", code)
	}
	for _, want := range []string{"decisions list", "decision show"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help omitted %q", want)
		}
	}
}

// Cancelling withdraws a question the human no longer wants answered. It is the
// operator's half of the decision lifecycle: without it a question asked in
// error blocks completion and cleanup forever, because only a worker resolution
// or a human cancellation can close one.
func TestCLI_CancellingADecisionWithdrawsIt(t *testing.T) {
	client := &fakeClient{}
	var output bytes.Buffer

	args := []string{"decision", "cancel", "task-0001", "schema-choice"}
	if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(decision cancel) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "cancel-decision:task-0001:schema-choice" {
		t.Errorf("client calls = %v, want one withdrawal", client.calls)
	}
}

func TestCLI_RefusesMalformedDecisionCancellations(t *testing.T) {
	for name, args := range map[string][]string{
		"no key":        {"decision", "cancel", "task-0001"},
		"bad handle":    {"decision", "cancel", "not a handle", "schema-choice"},
		"bad key":       {"decision", "cancel", "task-0001", "not a key"},
		"bad operation": {"decision", "cancel", "task-0001", "schema-choice", "--operation", "bad id"},
		"bad format":    {"decision", "cancel", "task-0001", "schema-choice", "--format", "text"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{}
			var output bytes.Buffer
			if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != ExitUsage {
				t.Fatalf("Run(%v) = %d, want %d: %s", args, code, ExitUsage, output.String())
			}
			if len(client.calls) != 0 {
				t.Fatalf("a malformed cancellation reached the service: %v", client.calls)
			}
		})
	}
}
