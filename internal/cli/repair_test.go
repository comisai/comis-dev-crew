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

func repairSurveyFixture() application.RepairSurvey {
	return application.RepairSurvey{
		SchemaVersion: 1, StateVersion: 9, CapturedAt: time.Unix(1_800_000_000, 0).UTC(),
		Tasks: []application.TaskRepair{
			{TaskHandle: "task-0001", State: domain.TaskUnknown, Posture: application.RepairReconcilable},
			{TaskHandle: "task-0004", State: domain.TaskUnknown, Posture: application.RepairWorktreeDirty},
			{TaskHandle: "task-0007", State: domain.TaskUnknown, Posture: application.RepairNoCandidate},
			{TaskHandle: "task-0009", State: domain.TaskUnknown, Posture: application.RepairAuthorityIncomplete},
			{TaskHandle: "task-0011", State: domain.TaskUnknown, Posture: application.RepairWorkspaceUnverified},
		},
	}
}

// The survey is only useful if it names the move. A posture with no stated next
// step leaves the operator to re-derive from the code what the service already
// knows, which is the friction this read exists to remove.
func TestCLI_TheRepairSurveyNamesTheNextSafeMoveForEveryPosture(t *testing.T) {
	client := &fakeClient{repairs: repairSurveyFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"repair", "reconcile"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(repair reconcile) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{
		"task-0001", "reconcilable", "task reconcile task-0001 --action validate-clean-candidate",
		"task-0004", "worktree_dirty", "task-0007", "no_candidate",
		"task-0009", "authority_incomplete", "task-0011", "workspace_unverified",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("survey omitted %q: %s", want, rendered)
		}
	}
	if len(client.calls) != 1 || client.calls[0] != "repairs:" {
		t.Errorf("client calls = %v, want one fleet-wide survey", client.calls)
	}
}

// The survey reports and never acts, so nothing it prints may be a mutation the
// command performed on the operator's behalf.
func TestCLI_TheRepairSurveyPerformsNoReconciliation(t *testing.T) {
	client := &fakeClient{repairs: repairSurveyFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"repair", "reconcile"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(repair reconcile) = %d: %s", code, output.String())
	}
	for _, call := range client.calls {
		if strings.HasPrefix(call, "reconcile:") {
			t.Fatalf("the survey reconciled a task: %v", client.calls)
		}
	}
}

// A quiet fleet says so rather than printing an empty table an operator has to
// interpret.
func TestCLI_TheRepairSurveyStatesWhenNothingNeedsRepair(t *testing.T) {
	client := &fakeClient{repairs: application.RepairSurvey{SchemaVersion: 1, StateVersion: 9}}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"repair", "reconcile"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(repair reconcile) = %d: %s", code, output.String())
	}
	if !strings.Contains(output.String(), "no task needs reconciliation") {
		t.Errorf("an empty survey did not say so: %s", output.String())
	}
}

func TestCLI_TheRepairSurveyScopesToOneTaskAtTheService(t *testing.T) {
	client := &fakeClient{repairs: repairSurveyFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"repair", "reconcile", "--task", "task-0001"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(repair reconcile --task) = %d: %s", code, output.String())
	}
	if len(client.calls) != 1 || client.calls[0] != "repairs:task-0001" {
		t.Errorf("client calls = %v, want the scoped survey", client.calls)
	}
}

func TestCLI_RefusesMalformedRepairInvocations(t *testing.T) {
	for name, args := range map[string][]string{
		"unknown verb":   {"repair", "everything"},
		"no verb":        {"repair"},
		"bare flag":      {"repair", "reconcile", "--task"},
		"bad handle":     {"repair", "reconcile", "--task", "not a handle"},
		"extra argument": {"repair", "reconcile", "task-0001"},
		"bad format":     {"repair", "reconcile", "--format", "yaml"},
	} {
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{repairs: repairSurveyFixture()}
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

func TestCLI_TheRepairSurveyOffersAStableJSONProjection(t *testing.T) {
	client := &fakeClient{repairs: repairSurveyFixture()}
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"repair", "reconcile", "--format", "json"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(repair reconcile --format json) = %d: %s", code, output.String())
	}
	if !strings.Contains(output.String(), "\"posture\": \"reconcilable\"") {
		t.Errorf("JSON output = %s", output.String())
	}
}

func TestCLI_DocumentsTheRepairCommand(t *testing.T) {
	var output bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, &output, &output, Config{}); code != 0 {
		t.Fatalf("Run(--help) = %d", code)
	}
	if !strings.Contains(output.String(), "repair reconcile") {
		t.Error("help omitted repair reconcile")
	}
}
