package reporter_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func TestRunCommand_ReadsBriefAndBuildsClosedSparseReportsFromProtectedCapability(t *testing.T) {
	brief := commandBrief()
	now := time.Date(2026, time.August, 10, 14, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		args    []string
		kind    domain.WorkerReportKind
		key     string
		summary string
		details string
	}{
		{name: "progress", args: []string{"progress", "--summary", "implemented parser"}, kind: domain.ReportProgress, summary: "implemented parser"},
		{name: "decision", args: []string{"decision", "--key", "database-choice", "--question", "Which database should be used?"}, kind: domain.ReportDecision, key: "database-choice", summary: "Which database should be used?"},
		{name: "blocked", args: []string{"blocked", "--summary", "dependency unavailable"}, kind: domain.ReportBlocked, summary: "dependency unavailable"},
		{name: "paused", args: []string{"paused", "--summary", "safe boundary reached"}, kind: domain.ReportPaused, summary: "safe boundary reached"},
		{name: "candidate", args: []string{"candidate-complete", "--summary", "candidate ready", "--artifact", "commit:abc123"}, kind: domain.ReportCandidateComplete, summary: "candidate ready", details: "artifact: commit:abc123"},
		{name: "failed", args: []string{"failed", "--summary", "validation failed"}, kind: domain.ReportFailed, summary: "validation failed"},
		{name: "resolved", args: []string{"resolved", "--key", "database-choice", "--summary", "applied the answer"}, kind: domain.ReportResolution, key: "database-choice", summary: "applied the answer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			capability := &commandCapability{brief: brief, receipt: domain.ReportReceipt{
				TaskHandle: "task-command-0001", LocalReportID: "report-command-0001",
				StateVersion: 4, AcceptedAt: now,
			}}
			var stdout, stderr bytes.Buffer
			exit := reporter.RunCommand(context.Background(), test.args, &stdout, &stderr, reporter.CommandConfig{
				Capability: capability, Clock: func() time.Time { return now },
				NewLocalReportID: func() (string, error) { return "report-command-0001", nil }, Version: "test",
			})
			if exit != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "report-command-0001") {
				t.Fatalf("RunCommand() = %d, stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
			report := capability.report
			if capability.briefCalls != 1 || capability.reportCalls != 1 || report.SchemaVersion != 1 ||
				report.LocalReportID != "report-command-0001" || report.BriefRevision != brief.Revision ||
				report.BriefRevisionHash != brief.RevisionHash || report.Kind != test.kind ||
				report.ExternalKey != test.key || report.Summary != test.summary || report.Details != test.details ||
				report.WorkerObservedAt == nil || !report.WorkerObservedAt.Equal(now) {
				t.Fatalf("submitted report = %#v", report)
			}
		})
	}
}

func TestRunCommand_BriefHelpAndVersionExposeNoAuthoritySelector(t *testing.T) {
	brief := commandBrief()
	capability := &commandCapability{brief: brief}
	var stdout, stderr bytes.Buffer
	exit := reporter.RunCommand(context.Background(), []string{"brief"}, &stdout, &stderr, reporter.CommandConfig{Capability: capability})
	if exit != 0 || stdout.String() != brief.Content || stderr.Len() != 0 || capability.briefCalls != 1 || capability.reportCalls != 0 {
		t.Fatalf("RunCommand(brief) = %d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	stdout.Reset()
	exit = reporter.RunCommand(context.Background(), []string{"--help"}, &stdout, &stderr, reporter.CommandConfig{Version: "test"})
	if exit != 0 || !strings.Contains(stdout.String(), "devcrew-report") {
		t.Fatalf("RunCommand(help) = %d stdout=%q", exit, stdout.String())
	}
	for _, forbidden := range []string{"--task", "--managed-run", "--workspace-lease", "--brief-hash", "--socket", "--credential"} {
		if strings.Contains(stdout.String(), forbidden) {
			t.Fatalf("help exposed authority selector %q: %q", forbidden, stdout.String())
		}
	}
	stdout.Reset()
	exit = reporter.RunCommand(context.Background(), []string{"--version"}, &stdout, &stderr, reporter.CommandConfig{Version: "test"})
	if exit != 0 || stdout.String() != "devcrew-report test\n" {
		t.Fatalf("RunCommand(version) = %d stdout=%q", exit, stdout.String())
	}
}

func TestRunCommand_AcknowledgesCanonicalWorkingDirectoryWithoutAuthoritySelectors(t *testing.T) {
	capability := &commandCapability{}
	var stdout, stderr bytes.Buffer
	exit := reporter.RunCommand(context.Background(), []string{"acknowledge"}, &stdout, &stderr, reporter.CommandConfig{
		Capability:       capability,
		WorkingDirectory: func() (string, error) { return "/canonical/task-worktree", nil },
	})
	if exit != 0 || stderr.Len() != 0 || capability.acknowledgeCalls != 1 ||
		capability.workingDirectory != "/canonical/task-worktree" || !strings.Contains(stdout.String(), "acknowledged") {
		t.Fatalf("RunCommand(acknowledge) = %d stdout=%q stderr=%q capability=%#v", exit, stdout.String(), stderr.String(), capability)
	}
	for _, args := range [][]string{
		{"acknowledge", "extra"},
		{"acknowledge", "--task", "task-other"},
		{"acknowledge", "--cwd", "/other"},
	} {
		if got := reporter.RunCommand(context.Background(), args, &stdout, &stderr, reporter.CommandConfig{Capability: capability}); got != 2 {
			t.Fatalf("RunCommand(%q) = %d, want invalid command", args, got)
		}
	}
}

func TestRunCommand_RejectsMalformedCommandsAndDependencyFailures(t *testing.T) {
	brief := commandBrief()
	tests := [][]string{
		nil,
		{"progress"},
		{"progress", "--summary", "unsafe\nsummary"},
		{"decision", "--key", "choice"},
		{"candidate-complete", "--summary", "ready"},
		{"resolved", "--key", "bad key", "--summary", "done"},
		{"progress", "--task", "task-other", "--summary", "attempt"},
		{"invented", "--summary", "attempt"},
	}
	for index, args := range tests {
		capability := &commandCapability{brief: brief}
		var stdout, stderr bytes.Buffer
		exit := reporter.RunCommand(context.Background(), args, &stdout, &stderr, reporter.CommandConfig{
			Capability: capability, Clock: func() time.Time { return time.Now().UTC() },
			NewLocalReportID: func() (string, error) { return "report-command-0001", nil },
		})
		if exit != 2 || stderr.Len() == 0 || capability.reportCalls != 0 {
			t.Fatalf("invalid command %d = %d stdout=%q stderr=%q reports=%d", index, exit, stdout.String(), stderr.String(), capability.reportCalls)
		}
	}

	privateFailure := fmt.Errorf("private attachment detail")
	for _, capability := range []*commandCapability{
		{brief: brief, briefErr: privateFailure},
		{brief: brief, reportErr: privateFailure},
	} {
		var stdout, stderr bytes.Buffer
		exit := reporter.RunCommand(context.Background(), []string{"progress", "--summary", "bounded"}, &stdout, &stderr, reporter.CommandConfig{
			Capability: capability, Clock: func() time.Time { return time.Now().UTC() },
			NewLocalReportID: func() (string, error) { return "report-command-0001", nil },
		})
		if exit != 1 || !strings.Contains(stderr.String(), "runtime attachment") || strings.Contains(stderr.String(), privateFailure.Error()) {
			t.Fatalf("dependency failure = %d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
		}
	}
}

type commandCapability struct {
	brief            domain.WorkerBrief
	receipt          domain.ReportReceipt
	report           domain.WorkerReport
	briefErr         error
	reportErr        error
	briefCalls       int
	reportCalls      int
	acknowledgeCalls int
	workingDirectory string
}

func (capability *commandCapability) Brief(context.Context) (domain.WorkerBrief, error) {
	capability.briefCalls++
	return capability.brief, capability.briefErr
}

func (capability *commandCapability) Report(_ context.Context, report domain.WorkerReport) (domain.ReportReceipt, error) {
	capability.reportCalls++
	capability.report = report
	return capability.receipt, capability.reportErr
}

func (capability *commandCapability) Acknowledge(_ context.Context, workingDirectory string) error {
	capability.acknowledgeCalls++
	capability.workingDirectory = workingDirectory
	return nil
}

func commandBrief() domain.WorkerBrief {
	content := "taskHandle: task-command-0001\nacceptanceCriteria:\n- prove command\n"
	return domain.WorkerBrief{
		Revision: 2, RevisionHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), Content: content,
	}
}
