package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func taskDiffFixture() application.TaskDiffView {
	return application.TaskDiffView{
		SchemaVersion: 1, StateVersion: 3, CapturedAt: time.Unix(1_800_000_000, 0).UTC(),
		TaskHandle: "task-0001", RepositoryID: "product-api",
		BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40),
		Committed: []application.TaskFileChange{
			{Path: "internal/thing.go", Added: 12, Deleted: 3},
			{Path: "assets/logo.png", Binary: true},
		},
		Uncommitted:       []application.TaskFileChange{{Path: "internal/other.go", Added: 4, Deleted: 1}},
		CommittedTotals:   application.TaskDiffTotals{Files: 2, Added: 12, Deleted: 3, BinaryFiles: 1},
		UncommittedTotals: application.TaskDiffTotals{Files: 1, Added: 4, Deleted: 1},
	}
}

// Committed and uncommitted work are shown apart because they mean different
// things: one is work the worker stands behind, the other is what a handback
// would land in a developer's editor.
func TestCLI_TheTaskDiffSeparatesCommittedFromUncommittedWork(t *testing.T) {
	client := &fakeClient{diff: taskDiffFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"task", "diff", "task-0001"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(task diff) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{"committed", "uncommitted", "internal/thing.go", "internal/other.go"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("diff omitted %q: %s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "binary") {
		t.Errorf("a binary change was rendered as a line count: %s", rendered)
	}
	if len(client.calls) != 1 || client.calls[0] != "diff:task-0001" {
		t.Errorf("client calls = %v, want one diff read", client.calls)
	}
}

// --name-only drops the counts, and --stat keeps them. Both stay bounded
// summaries; a patch body is unbounded worker content this surface never carries.
func TestCLI_TheTaskDiffOffersBoundedSummariesOnly(t *testing.T) {
	client := &fakeClient{diff: taskDiffFixture()}
	var stat bytes.Buffer
	if code := Run(context.Background(), []string{"task", "diff", "task-0001", "--stat"}, &stat, &stat, testConfig(client)); code != 0 {
		t.Fatalf("Run(task diff --stat) = %d: %s", code, stat.String())
	}
	if !strings.Contains(stat.String(), "12") {
		t.Errorf("--stat dropped the line counts: %s", stat.String())
	}

	client = &fakeClient{diff: taskDiffFixture()}
	var names bytes.Buffer
	if code := Run(context.Background(), []string{"task", "diff", "task-0001", "--name-only"}, &names, &names, testConfig(client)); code != 0 {
		t.Fatalf("Run(task diff --name-only) = %d: %s", code, names.String())
	}
	if !strings.Contains(names.String(), "internal/thing.go") {
		t.Errorf("--name-only dropped the paths: %s", names.String())
	}
	if strings.Contains(names.String(), "+12") {
		t.Errorf("--name-only carried line counts: %s", names.String())
	}
}

// A truncated listing says so. Presenting a partial file list as complete would
// let an operator decide from a change set larger than what they were shown.
func TestCLI_TheTaskDiffStatesWhenItsListingIsPartial(t *testing.T) {
	view := taskDiffFixture()
	view.FileListTruncated = true
	client := &fakeClient{diff: view}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"task", "diff", "task-0001"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(task diff) = %d: %s", code, output.String())
	}
	if !strings.Contains(output.String(), "truncated") {
		t.Errorf("a truncated listing did not say so: %s", output.String())
	}
}

func TestCLI_RefusesMalformedTaskDiffInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"no task":         {"task", "diff"},
		"bad handle":      {"task", "diff", "not a handle"},
		"both selectors":  {"task", "diff", "task-0001", "--stat", "--name-only"},
		"unknown flag":    {"task", "diff", "task-0001", "--patch"},
		"unsupported fmt": {"task", "diff", "task-0001", "--format", "yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{diff: taskDiffFixture()}
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

func TestCLI_TheTaskDiffOffersAStableJSONProjection(t *testing.T) {
	client := &fakeClient{diff: taskDiffFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"task", "diff", "task-0001", "--format", "json"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(task diff --format json) = %d: %s", code, output.String())
	}
	if !strings.Contains(output.String(), "\"committedTotals\"") {
		t.Errorf("JSON output = %s", output.String())
	}
}

func TestCLI_DocumentsTheTaskDiffCommand(t *testing.T) {
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &output, &output, Config{}); code != 0 {
		t.Fatalf("Run(--help) = %d", code)
	}
	if !strings.Contains(output.String(), "task diff") {
		t.Error("help omitted task diff")
	}
}
