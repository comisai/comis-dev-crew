package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func taskLogPageFixture() application.TaskLogPage {
	occurred := time.Date(2026, time.August, 19, 9, 0, 0, 0, time.UTC)
	return application.TaskLogPage{
		SchemaVersion: 1, CapturedAt: occurred, TaskHandle: "task-0001",
		Source: application.LogSourceWorker, NextCursor: 4,
		Entries: []application.TaskLogEntry{
			{Sequence: 3, OccurredAt: occurred, Source: application.LogSourceWorker,
				Label: "progress", Detail: "reworked the migration order"},
			{Sequence: 4, OccurredAt: occurred, Source: application.LogSourceWorker,
				Label: "decision", Detail: "which migration order applies", Outcome: "schema-choice"},
		},
	}
}

func TestCLI_TaskLogsPrintsTheWorkerAccountByDefault(t *testing.T) {
	client := &fakeClient{logs: taskLogPageFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"task", "logs", "task-0001"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(task logs) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{"progress", "reworked the migration order", "decision"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("logs omitted %q: %s", want, rendered)
		}
	}
	if len(client.calls) != 1 || client.calls[0] != "logs:task-0001:worker:0" {
		t.Errorf("client calls = %v, want the worker account from the start", client.calls)
	}
}

// Each named source is passed through untouched, so the CLI can never answer a
// question the operator did not ask from a different record.
func TestCLI_TaskLogsPassesEverySourceThrough(t *testing.T) {
	for _, source := range []string{"worker", "service", "validation"} {
		client := &fakeClient{logs: taskLogPageFixture()}
		var output bytes.Buffer
		args := []string{"task", "logs", "task-0001", "--source", source}
		if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != 0 {
			t.Fatalf("Run(%v) = %d: %s", args, code, output.String())
		}
		if len(client.calls) != 1 || client.calls[0] != "logs:task-0001:"+source+":0" {
			t.Errorf("client calls = %v, want source %q", client.calls, source)
		}
	}
}

// --follow bounds itself so the command terminates, and each pass resumes from
// the cursor the previous page handed back rather than re-reading from zero.
func TestCLI_TaskLogsFollowResumesFromTheReturnedCursor(t *testing.T) {
	client := &fakeClient{logs: taskLogPageFixture()}
	var output bytes.Buffer

	args := []string{"task", "logs", "task-0001", "--follow", "--passes", "2"}
	if code := Run(context.Background(), args, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(task logs --follow) = %d: %s", code, output.String())
	}
	if len(client.calls) != 2 {
		t.Fatalf("client calls = %v, want one per pass", client.calls)
	}
	if client.calls[0] != "logs:task-0001:worker:0" || client.calls[1] != "logs:task-0001:worker:4" {
		t.Errorf("client calls = %v, want the second pass resumed at the returned cursor", client.calls)
	}
}

func TestCLI_RefusesMalformedTaskLogInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"no task":        {"task", "logs"},
		"bad handle":     {"task", "logs", "not a handle"},
		"unknown source": {"task", "logs", "task-0001", "--source", "terminal"},
		"bare source":    {"task", "logs", "task-0001", "--source"},
		"bad format":     {"task", "logs", "task-0001", "--format", "yaml"},
		"bad passes":     {"task", "logs", "task-0001", "--follow", "--passes", "0"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{logs: taskLogPageFixture()}
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

func TestCLI_DocumentsTheTaskLogsCommand(t *testing.T) {
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &output, &output, Config{}); code != 0 {
		t.Fatalf("Run(--help) = %d", code)
	}
	if !strings.Contains(output.String(), "task logs") {
		t.Error("help omitted task logs")
	}
}
