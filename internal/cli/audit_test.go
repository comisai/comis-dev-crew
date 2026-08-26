package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func auditPage() application.AuditPage {
	observed := time.Date(2026, time.August, 19, 9, 30, 0, 0, time.UTC)
	return application.AuditPage{
		SchemaVersion: 1, CapturedAt: observed, NextCursor: 2,
		Events: []application.AuditEvent{
			{
				Sequence: 1, OccurredAt: observed, Kind: application.AuditCleanupRefused,
				TaskHandle: "task-0001", Reason: application.AuditCleanupOpenDecision,
			},
			{
				Sequence: 2, OccurredAt: observed, Kind: application.AuditReportAuthenticationFailed,
				TaskHandle: "task-0002", Reason: application.AuditCredentialMismatch,
			},
		},
	}
}

func TestCLI_AuditTailRendersTheTrailAndItsResumeCursor(t *testing.T) {
	client := &fakeClient{audit: auditPage()}
	var output bytes.Buffer
	code := Run(context.Background(), []string{"audit", "tail"}, &output, &output, Config{
		DefaultSocketPath: "/tmp/devcrew.sock", Version: "test",
		NewClient:      func(string) (ReadClient, error) { return client, nil },
		NewOperationID: func() (string, error) { return "operation-audit-0001", nil },
	})
	if code != ExitSuccess {
		t.Fatalf("Run() = %d, want success; output %q", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{
		"cleanup_refused", "open_decision", "report_authentication_failed",
		"credential_mismatch", "task-0001", "resume with --after 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("audit output missing %q; got %q", want, rendered)
		}
	}
}

func TestCLI_AuditTailResumesFromACursor(t *testing.T) {
	client := &fakeClient{audit: auditPage()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"audit", "tail", "--after", "7", "--format", "jsonl"},
		&output, &output, Config{
			DefaultSocketPath: "/tmp/devcrew.sock", Version: "test",
			NewClient:      func(string) (ReadClient, error) { return client, nil },
			NewOperationID: func() (string, error) { return "operation-audit-0002", nil },
		}); code != ExitSuccess {
		t.Fatalf("Run() = %d, want success; output %q", code, output.String())
	}
	if !strings.Contains(strings.Join(client.calls, " "), "audit:7") {
		t.Errorf("cursor was not forwarded; calls = %v", client.calls)
	}
	if strings.Contains(output.String(), "resume with") {
		t.Error("jsonl output carried the human resume line")
	}
}

func TestCLI_AuditTailRefusesUnknownShapes(t *testing.T) {
	for _, args := range [][]string{
		{"audit"},
		{"audit", "list"},
		{"audit", "tail", "--after", "-1"},
		{"audit", "tail", "--format", "table"},
		{"audit", "tail", "--task", "task-0001"},
	} {
		var output bytes.Buffer
		if code := Run(context.Background(), args, &output, &output, Config{
			DefaultSocketPath: "/tmp/devcrew.sock", Version: "test",
			NewClient:      func(string) (ReadClient, error) { return &fakeClient{}, nil },
			NewOperationID: func() (string, error) { return "operation-audit-0003", nil },
		}); code == ExitSuccess {
			t.Errorf("Run(%v) succeeded, want a refusal", args)
		}
	}
}

// TestCLI_AuditTailReportsAnUnwritableDestination keeps a failed render from
// exiting as if the trail had been shown.
func TestCLI_AuditTailReportsAnUnwritableDestination(t *testing.T) {
	for _, format := range []string{"text", "jsonl"} {
		client := &fakeClient{audit: auditPage()}
		var errorOutput bytes.Buffer
		code := Run(context.Background(), []string{"audit", "tail", "--format", format},
			failingWriter{}, &errorOutput, Config{
				DefaultSocketPath: "/tmp/devcrew.sock", Version: "test",
				NewClient:      func(string) (ReadClient, error) { return client, nil },
				NewOperationID: func() (string, error) { return "operation-audit-0004", nil },
			})
		if code == ExitSuccess {
			t.Errorf("Run(%s) reported success despite an unwritable destination", format)
		}
	}
}

// TestCLI_AuditTailRendersAnEmptyTrail proves the absent-value placeholder and
// the cursor still reach an operator who has nothing to read yet.
func TestCLI_AuditTailRendersAnEmptyTrail(t *testing.T) {
	client := &fakeClient{audit: application.AuditPage{SchemaVersion: 1, NextCursor: 0, Events: []application.AuditEvent{
		{Sequence: 1, OccurredAt: time.Now().UTC(), Kind: application.AuditCleanupRefused, Reason: application.AuditCleanupOpenHold},
	}}}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"audit", "tail"}, &output, &output, Config{
		DefaultSocketPath: "/tmp/devcrew.sock", Version: "test",
		NewClient:      func(string) (ReadClient, error) { return client, nil },
		NewOperationID: func() (string, error) { return "operation-audit-0005", nil },
	}); code != ExitSuccess {
		t.Fatalf("Run() = %d, want success; output %q", code, output.String())
	}
	if !strings.Contains(output.String(), "-") {
		t.Errorf("an absent task handle was not rendered as a placeholder: %q", output.String())
	}
}
