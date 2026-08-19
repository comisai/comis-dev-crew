package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func eventPageFixture() application.EventPage {
	occurred := time.Date(2026, time.August, 19, 9, 0, 0, 0, time.UTC)
	return application.EventPage{
		SchemaVersion: 1, CapturedAt: occurred, NextCursor: 9,
		Events: []application.ServiceEvent{
			{Sequence: 8, OccurredAt: occurred, Kind: application.EventTaskStateChanged,
				TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 4},
			{Sequence: 9, OccurredAt: occurred, Kind: application.EventDecisionOpened,
				TaskHandle: "task-0001", Reason: "schema-choice"},
		},
	}
}

// One pass prints what has happened and stops. Following is opt-in, so a script
// piping the stream is not left hanging on a command it expected to return.
func TestCLI_EventsTailPrintsOnePassAndReturns(t *testing.T) {
	client := &fakeClient{events: eventPageFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"events", "tail"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(events tail) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{"task-0001", "task_state_changed", "decision_opened", "schema-choice", "working"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("stream omitted %q: %s", want, rendered)
		}
	}
	if len(client.calls) != 1 || client.calls[0] != "events:0:" {
		t.Errorf("client calls = %v, want one read from the start", client.calls)
	}
}

// A caller resumes from a cursor it already holds, so a follower restarting
// replays nothing it has already displayed.
func TestCLI_EventsTailResumesFromTheGivenCursor(t *testing.T) {
	client := &fakeClient{events: eventPageFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"events", "tail", "--after", "4"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(events tail --after) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "events:4:" {
		t.Errorf("client calls = %v, want the resumed cursor", client.calls)
	}
}

// The stream is content-free by contract, so the rendering must not invent a
// column that could carry task content.
func TestCLI_EventsTailRendersNoTaskContent(t *testing.T) {
	client := &fakeClient{events: eventPageFixture()}
	for _, format := range []string{"text", "jsonl"} {
		var output bytes.Buffer
		if code := Run(context.Background(), []string{"events", "tail", "--format", format}, &output, &output, testConfig(client)); code != 0 {
			t.Fatalf("Run(events tail --format %s) = %d: %s", format, code, output.String())
		}
		lowered := strings.ToLower(output.String())
		for _, forbidden := range []string{"objective", "summary", "details", "branch", "worktree", "/home/", "repositoryid"} {
			if strings.Contains(lowered, forbidden) {
				t.Errorf("%s output carried %q: %s", format, forbidden, output.String())
			}
		}
	}
}

// jsonl is one event per line so a follower can consume it incrementally
// without waiting for a document to close.
func TestCLI_EventsTailEmitsOneEventPerLineInJSONL(t *testing.T) {
	client := &fakeClient{events: eventPageFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"events", "tail", "--format", "jsonl"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(events tail --format jsonl) = %d: %s", code, output.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("jsonl lines = %d, want one per event: %s", len(lines), output.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, "\"sequence\"") {
			t.Errorf("jsonl line is not one event object: %q", line)
		}
	}
}

func TestCLI_RefusesMalformedEventInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown verb":    {"events", "follow"},
		"no verb":         {"events"},
		"bare after":      {"events", "tail", "--after"},
		"negative after":  {"events", "tail", "--after", "-1"},
		"after not a num": {"events", "tail", "--after", "x"},
		"bad format":      {"events", "tail", "--format", "table"},
		"extra argument":  {"events", "tail", "task-0001"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{events: eventPageFixture()}
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

// status --watch re-reads the authoritative snapshot each pass rather than
// mutating a display from events. A dropped event can then never leave the view
// falsely current, which is the whole reason the snapshot stays authoritative.
func TestCLI_StatusWatchRefreshesTheAuthoritativeSnapshot(t *testing.T) {
	client := &fakeClient{fleet: application.FleetSnapshot{SchemaVersion: 1, StateVersion: 3}}
	var output bytes.Buffer

	code := Run(context.Background(), []string{"status", "--watch", "--passes", "3"}, &output, &output, testConfig(client))
	if code != 0 {
		t.Fatalf("Run(status --watch) = %d: %s", code, output.String())
	}
	fleetReads := 0
	for _, call := range client.calls {
		if call == "fleet" {
			fleetReads++
		}
	}
	if fleetReads != 3 {
		t.Fatalf("fleet reads = %d, want one authoritative snapshot per pass: %v", fleetReads, client.calls)
	}
}

func TestCLI_DocumentsTheEventAndWatchSurface(t *testing.T) {
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &output, &output, Config{}); code != 0 {
		t.Fatalf("Run(--help) = %d", code)
	}
	for _, want := range []string{"events tail", "--watch"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("help omitted %q", want)
		}
	}
}

// An operator following one task should not have to read the whole fleet's
// stream to find it. The scope travels to the service rather than filtering a
// full page locally, so a busy fleet cannot push one task's events off the page
// before the console ever sees them.
func TestCLI_ScopesTheEventStreamToOneTask(t *testing.T) {
	client := &fakeClient{}
	var output bytes.Buffer
	args := []string{"events", "tail", "--task", "task-0001"}
	if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(events tail --task) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "events:0:task-0001" {
		t.Fatalf("client calls = %v, want a task-scoped read", client.calls)
	}
}

func TestCLI_RefusesAnInvalidEventTaskScope(t *testing.T) {
	client := &fakeClient{}
	var output bytes.Buffer
	args := []string{"events", "tail", "--task", "not a handle"}
	if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != ExitUsage {
		t.Fatalf("Run(events tail bad task) = %d, want %d", code, ExitUsage)
	}
	if len(client.calls) != 0 {
		t.Fatalf("an invalid scope reached the service: %v", client.calls)
	}
}
