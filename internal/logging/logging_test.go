package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func decodeLines(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		lines = append(lines, decoded)
	}
	return lines
}

func TestLogger_WritesOneStructuredLinePerCrossing(t *testing.T) {
	var destination bytes.Buffer
	logger, err := New(&destination, LevelDebug)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Record(application.BoundaryRecord{
		Boundary: application.BoundaryLocalAPI, Operation: "ListTasks",
		OperationID: "operation-0001", TaskHandle: "task-0001",
		DurationMs: 12, Outcome: application.BoundaryCompleted,
	})
	lines := decodeLines(t, destination.String())
	if len(lines) != 1 {
		t.Fatalf("wrote %d lines, want 1", len(lines))
	}
	line := lines[0]
	for key, want := range map[string]any{
		"boundary": "local_api", "operation": "ListTasks", "outcome": "completed",
		"operationId": "operation-0001", "taskHandle": "task-0001",
		"durationMs": float64(12), "level": "INFO",
	} {
		if line[key] != want {
			t.Errorf("line[%q] = %v, want %v", key, line[key], want)
		}
	}
}

// TestLogger_SeparatesStepsFailuresAndCompletions keeps the levels usable. An
// operator paging on ERROR needs failures there and progress somewhere else.
func TestLogger_SeparatesStepsFailuresAndCompletions(t *testing.T) {
	var destination bytes.Buffer
	logger, err := New(&destination, LevelDebug)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Record(application.BoundaryRecord{
		Boundary: application.BoundaryReporter, Operation: "accept",
		Outcome: application.BoundaryStep,
	})
	logger.Record(application.BoundaryRecord{
		Boundary: application.BoundaryControl, Operation: "handshake",
		Outcome: application.BoundaryFailed, ErrorKind: domain.ErrorUnavailable,
		Hint: "inspect the control connection",
	})
	lines := decodeLines(t, destination.String())
	if len(lines) != 2 {
		t.Fatalf("wrote %d lines, want 2", len(lines))
	}
	if lines[0]["level"] != "DEBUG" {
		t.Errorf("step level = %v, want DEBUG", lines[0]["level"])
	}
	if lines[1]["level"] != "ERROR" {
		t.Errorf("failure level = %v, want ERROR", lines[1]["level"])
	}
	if lines[1]["errorKind"] != "unavailable" || lines[1]["hint"] != "inspect the control connection" {
		t.Errorf("failure line lost its kind or hint: %v", lines[1])
	}
	if _, present := lines[0]["errorKind"]; present {
		t.Error("a step carried an error kind")
	}
}

func TestLogger_HonoursTheSelectedLevel(t *testing.T) {
	var destination bytes.Buffer
	logger, err := New(&destination, LevelWarn)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	logger.Record(application.BoundaryRecord{
		Boundary: application.BoundaryLocalAPI, Operation: "ListTasks",
		Outcome: application.BoundaryCompleted,
	})
	logger.Record(application.BoundaryRecord{
		Boundary: application.BoundaryLocalAPI, Operation: "ListTasks",
		Outcome: application.BoundaryStep,
	})
	if strings.TrimSpace(destination.String()) != "" {
		t.Errorf("warn level wrote lower-severity lines: %q", destination.String())
	}
	logger.Record(application.BoundaryRecord{
		Boundary: application.BoundaryLocalAPI, Operation: "ListTasks",
		Outcome: application.BoundaryFailed, ErrorKind: domain.ErrorInternal, Hint: "inspect service health",
	})
	if len(decodeLines(t, destination.String())) != 1 {
		t.Errorf("warn level dropped a failure: %q", destination.String())
	}
}

func TestParseLevel_AcceptsTheClosedSetAndRefusesTheRest(t *testing.T) {
	for _, value := range []string{"debug", "INFO", " warn ", "Error"} {
		if _, err := ParseLevel(value); err != nil {
			t.Errorf("ParseLevel(%q) error = %v", value, err)
		}
	}
	for _, value := range []string{"", "trace", "verbose", "off"} {
		if _, err := ParseLevel(value); err == nil {
			t.Errorf("ParseLevel(%q) accepted an unknown level", value)
		}
	}
}

func TestNew_RefusesAnUnusableConfiguration(t *testing.T) {
	if _, err := New(nil, LevelInfo); err == nil {
		t.Error("New() accepted an absent destination")
	}
	if _, err := New(&bytes.Buffer{}, Level("trace")); err == nil {
		t.Error("New() accepted an unknown level")
	}
}

func TestLogger_StaysSilentWhenUnbuilt(t *testing.T) {
	var absent *Logger
	absent.Record(application.BoundaryRecord{Boundary: application.BoundaryLocalAPI})
	(&Logger{}).Record(application.BoundaryRecord{Boundary: application.BoundaryLocalAPI})
}
