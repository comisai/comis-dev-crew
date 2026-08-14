package livecampaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type fixtureExecutor struct {
	manifest            Manifest
	currentBaseRevision string
	systemHealth        []byte
	explanation         []byte
	operationUpdatedAt  map[string]int64
	calls               []Command
}

func (executor *fixtureExecutor) Run(_ context.Context, command Command) ([]byte, error) {
	executor.calls = append(executor.calls, command)
	args := strings.Join(command.Args, " ")
	manifest := executor.manifest
	encode := func(value any) ([]byte, error) { return json.Marshal(value) }
	if command.Path == manifest.DevCrew.CLIPath {
		switch {
		case args == "--socket "+manifest.DevCrew.SocketPath+" service status":
			return []byte("SERVICE HEALTH COMPLETENESS\ndevcrew-service healthy complete\n"), nil
		case args == "--socket "+manifest.DevCrew.SocketPath+" doctor --format json":
			return encode(application.DiagnosticReport{SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, StateVersion: 20, Completeness: application.CompletenessComplete, ServiceHealth: application.HealthHealthy, ComisHealth: application.HealthHealthy})
		case args == "--socket "+manifest.DevCrew.SocketPath+" status --format json":
			return encode(application.FleetSnapshot{SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, StateVersion: 20, Completeness: application.CompletenessComplete, ServiceHealth: application.HealthHealthy, ComisHealth: application.HealthHealthy, Tasks: []application.TaskSummary{acceptedTask(manifest.Tasks[0]).Summary, acceptedTask(manifest.Tasks[1]).Summary}})
		}
		for _, task := range manifest.Tasks {
			detail := acceptedTask(task)
			if task.TaskHandle == manifest.Tasks[1].TaskHandle {
				detail.Summary.Head = strings.Repeat("e", 40)
				detail.Evidence.Candidate.HeadRevision = detail.Summary.Head
				detail.Evidence.Delivery.PullRequestID = "github-pr-18"
				detail.Evidence.Cleanup.OperationID = "operation-cleanup-claude"
			}
			if args == "--socket "+manifest.DevCrew.SocketPath+" task show "+task.TaskHandle+" --format json" {
				return encode(detail)
			}
			if args == "--socket "+manifest.DevCrew.SocketPath+" task explain "+task.TaskHandle+" --format json" {
				return encode(application.TaskExplanation{SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, Completeness: application.CompletenessComplete, Summary: detail.Summary, Evidence: detail.Evidence, ReasonCode: "task_cleaned", Explanation: "Task cleanup is complete.", LikelyRootCause: "none", NextSafeActions: []application.NextAction{application.ActionNone}})
			}
		}
		for _, operation := range manifest.Operations {
			if args == "--socket "+manifest.DevCrew.SocketPath+" task operation "+operation.OperationID+" --format json" {
				updatedAt := manifest.EndedAtMs
				if executor.operationUpdatedAt != nil && executor.operationUpdatedAt[operation.OperationID] != 0 {
					updatedAt = executor.operationUpdatedAt[operation.OperationID]
				}
				return encode(application.OperationView{SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, OperationID: operation.OperationID, Command: operation.Command, SubjectDigest: strings.Repeat("f", 64), Status: domain.OperationCompleted, StateVersion: 20, CreatedAtMs: manifest.StartedAtMs, UpdatedAtMs: updatedAt})
			}
		}
	}
	if command.Path == manifest.Comis.NodePath && len(command.Args) > 1 && command.Args[0] == manifest.Comis.CLIScriptPath {
		switch command.Args[1] {
		case "messages":
			return encode(completeMessages(manifest))
		case "system-health":
			if executor.systemHealth != nil {
				return executor.systemHealth, nil
			}
			return encode(validSystemHealth(manifest))
		case "explain":
			if executor.explanation != nil {
				return executor.explanation, nil
			}
			return encode(validIncident(manifest))
		case "secrets":
			return []byte("[]"), nil
		}
	}
	if command.Path == manifest.Comis.NodePath && len(command.Args) > 0 && command.Args[0] == manifest.Comis.SecretResidencyScript {
		return []byte(`{"schemaVersion":1,"scannedFiles":10,"readErrors":[],"totalMatches":0,"secrets":{"TELEGRAM_BOT_TOKEN":{"retrieved":true,"totalMatches":0},"GITHUB_TOKEN":{"retrieved":true,"totalMatches":0}}}`), nil
	}
	if command.Path == manifest.GitHub.GitPath {
		if strings.Contains(args, "status --porcelain=v1") {
			return []byte{}, nil
		}
		if strings.Contains(args, "rev-parse "+manifest.GitHub.BaseBranch) {
			revision := executor.currentBaseRevision
			if revision == "" {
				revision = strings.Repeat("d", 40)
			}
			return []byte(revision + "\n"), nil
		}
		if strings.Contains(args, "merge-base --is-ancestor") {
			return []byte{}, nil
		}
	}
	if command.Path == manifest.GitHub.CLIPath {
		for index, task := range manifest.Tasks {
			number := 17 + index
			head := strings.Repeat("a", 40)
			if index == 1 {
				head = strings.Repeat("e", 40)
			}
			if args == "api repos/"+manifest.GitHub.Repository+"/pulls/"+strconv.Itoa(number) {
				pull := GitHubPull{Number: number, State: "open", HTMLURL: "https://github.com/" + manifest.GitHub.Repository + "/pull/" + strconv.Itoa(number)}
				pull.Head.SHA, pull.Head.Ref, pull.Base.Ref = head, "devcrew/"+task.TaskHandle, manifest.GitHub.BaseBranch
				return encode(pull)
			}
			if args == "api -H Accept: application/vnd.github+json repos/"+manifest.GitHub.Repository+"/commits/"+head+"/check-runs" {
				success := "success"
				return encode(GitHubChecks{Runs: []GitHubCheckRun{{Name: "validate", Status: "completed", Conclusion: &success}}})
			}
		}
	}
	return nil, errors.New("unexpected command: " + command.Path + " " + args)
}

func validSystemHealth(manifest Manifest) comisSystemHealthReport {
	zero := 0
	zeroRate := 0.0
	report := comisSystemHealthReport{
		SchemaVersion: 1, WindowHours: 24, DegradedByCause: map[string]int{},
		ToolStats: map[string]comisToolCount{"reconcile_task": {OK: 1}},
		Findings: []struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
			Count  int    `json:"count"`
			Hint   string `json:"hint"`
		}{},
		LikelyRootCause: json.RawMessage("null"), SuggestedNextSteps: []string{},
		Truncations: []struct {
			Field  string `json:"field"`
			Reason string `json:"reason"`
		}{},
	}
	report.Sessions.Total = 2
	report.Sessions.DeliveredWithToolErrors = &zero
	report.Sessions.HardDegraded = &zero
	report.Sessions.HardDegradedRate = &zeroRate
	report.TopErrorKinds = []struct {
		Kind  string `json:"kind"`
		Count int    `json:"count"`
	}{}
	report.Activity.ActiveAgents = []string{manifest.Comis.AgentID}
	report.Activity.ActiveChannels = []string{"telegram"}
	report.Activity.ExitReasons = map[string]int{"completed": 2}
	report.Activity.TurnTotal = 2
	report.Coverage = &struct {
		SessionSummary struct {
			Found bool `json:"found"`
			Rows  int  `json:"rows"`
		} `json:"sessionSummary"`
		SessionIndex struct {
			DaysRead    int `json:"daysRead"`
			DaysMissing int `json:"daysMissing"`
		} `json:"sessionIndex"`
		Billing struct {
			Present bool `json:"present"`
		} `json:"billing"`
	}{}
	report.Coverage.SessionSummary.Found = true
	report.Coverage.SessionSummary.Rows = 2
	report.Coverage.SessionIndex.DaysRead = 1
	report.Coverage.Billing.Present = true
	return report
}

func validIncident(manifest Manifest) comisIncidentReport {
	report := comisIncidentReport{
		SchemaVersion: 1, SessionKey: manifest.Comis.ExplainRefs[0], TraceID: "trace-live-0001",
		AgentID: manifest.Comis.AgentID,
		ToolStats: map[string]comisToolCount{
			"prepare_task": {OK: 2}, "list_tasks": {OK: 1}, "get_task": {OK: 2},
			"explain_task": {OK: 1}, "get_launch_plan": {OK: 2}, "reconcile_task": {OK: 1},
			"handback_task": {OK: 1}, "cleanup_task": {OK: 2, Failed: 2},
		},
		Failures: []struct {
			Seq          int    `json:"seq"`
			ToolName     string `json:"toolName"`
			ErrorKind    string `json:"errorKind"`
			FailureCode  string `json:"failureCode"`
			ErrorPreview string `json:"errorPreview"`
		}{
			{Seq: 1, ToolName: "cleanup_task", ErrorKind: "precondition", FailureCode: "precondition", ErrorPreview: application.CleanupOpenDecisionMessage},
			{Seq: 2, ToolName: "cleanup_task", ErrorKind: "precondition", FailureCode: "precondition", ErrorPreview: application.CleanupDirtyWorkspaceMessage},
		},
		BreakerTimeline: []json.RawMessage{}, Offloads: []json.RawMessage{}, Summary: "Campaign completed.",
		LikelyRootCause: json.RawMessage("null"), SuggestedNextSteps: []string{}, Truncations: []json.RawMessage{},
	}
	report.Channel.Type = "telegram"
	report.Channel.ID = manifest.Telegram.OriginChatID
	report.Outcome.EndReason = "completed"
	report.Outcome.Severity = "ok"
	report.Timing.DurationMs = 1000
	report.Timing.TurnCount = 1
	report.Coverage = &struct {
		Trajectory struct {
			Found   bool `json:"found"`
			Records int  `json:"records"`
		} `json:"trajectory"`
		Rollup struct {
			Present bool `json:"present"`
		} `json:"rollup"`
		Offloads struct {
			PointersResolved int `json:"pointersResolved"`
			PointersTotal    int `json:"pointersTotal"`
		} `json:"offloads"`
		LosslessContext *struct {
			Found       bool `json:"found"`
			ToolResults int  `json:"toolResults"`
		} `json:"losslessContext"`
	}{}
	report.Coverage.Trajectory.Found = true
	report.Coverage.Trajectory.Records = 1
	report.Coverage.Rollup.Present = true
	return report
}

func TestCollectAcceptsSharedPinnedTaskBaseWhenCanonicalBranchAdvances(t *testing.T) {
	manifest := validManifest()
	executor := &fixtureExecutor{manifest: manifest, currentBaseRevision: strings.Repeat("f", 40)}
	root := filepath.Join(t.TempDir(), "evidence")
	verdict, err := Collect(context.Background(), manifest, root, executor, manifest.EndedAtMs)
	if err != nil || !verdict.Passed {
		t.Fatalf("Collect(advanced canonical base) = %#v, %v", verdict, err)
	}
	wantAncestorCheck := "merge-base --is-ancestor " + strings.Repeat("d", 40) + " " + strings.Repeat("f", 40)
	found := false
	for _, call := range executor.calls {
		if call.Path == manifest.GitHub.GitPath && strings.Contains(strings.Join(call.Args, " "), wantAncestorCheck) {
			found = true
		}
	}
	if !found {
		t.Fatalf("Git calls = %#v, want pinned-base ancestry check", executor.calls)
	}
	contents, err := os.ReadFile(filepath.Join(verdict.EvidenceDirectory, "git-truth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var truth gitTruth
	if err := json.Unmarshal(contents, &truth); err != nil {
		t.Fatal(err)
	}
	if truth.TaskBaseRevision != strings.Repeat("d", 40) ||
		truth.CurrentBaseRevision != strings.Repeat("f", 40) {
		t.Fatalf("Git truth = %#v, want distinct pinned and current base revisions", truth)
	}
}

func TestCollectWritesPrivateBoundedEvidenceAndPassingVerdict(t *testing.T) {
	manifest := validManifest()
	executor := &fixtureExecutor{manifest: manifest}
	root := filepath.Join(t.TempDir(), "evidence")
	verdict, err := Collect(context.Background(), manifest, root, executor, manifest.EndedAtMs)
	if err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	if !verdict.Passed || verdict.CampaignID != manifest.CampaignID || len(verdict.Checks) < 7 {
		t.Fatalf("verdict = %#v", verdict)
	}
	info, err := os.Stat(verdict.EvidenceDirectory)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("evidence directory mode = %v, %v", info, err)
	}
	for _, name := range []string{"manifest.json", "devcrew-service-status.txt", "devcrew-fleet.json", "telegram-checkpoints.json", "git-truth.json", "secret-residency.json", "hashes.json", "verdict.json"} {
		info, err := os.Stat(filepath.Join(verdict.EvidenceDirectory, name))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("artifact %s mode = %v, %v", name, info, err)
		}
	}
	checkpointContents, err := os.ReadFile(filepath.Join(verdict.EvidenceDirectory, "telegram-checkpoints.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, checkpoint := range manifest.Telegram.Checkpoints {
		if strings.Contains(string(checkpointContents), checkpoint.Marker) {
			t.Fatalf("checkpoint artifact retained message marker %s", checkpoint.Marker)
		}
	}
}

func TestCollectRefusesStructurallyEmptyComisObservabilityReports(t *testing.T) {
	for _, test := range []struct {
		name          string
		health        []byte
		explanation   []byte
		wantErrorPart string
	}{
		{name: "system health", health: []byte(`{}`), wantErrorPart: "system health evidence refused"},
		{name: "session explanation", explanation: []byte(`{}`), wantErrorPart: "Comis explanation 1 evidence refused"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifest := validManifest()
			executor := &fixtureExecutor{
				manifest: manifest, systemHealth: test.health, explanation: test.explanation,
			}
			_, err := Collect(
				context.Background(), manifest, filepath.Join(t.TempDir(), "evidence"), executor, manifest.EndedAtMs,
			)
			if err == nil || !strings.Contains(err.Error(), test.wantErrorPart) {
				t.Fatalf("Collect() error = %v, want %q", err, test.wantErrorPart)
			}
		})
	}
}

func TestCollectRefusesIncompleteComisCoverageAndTelegramAuthority(t *testing.T) {
	manifest := validManifest()
	healthWithoutCoverage := validSystemHealth(manifest)
	healthWithoutCoverage.Coverage = nil
	incidentFromNewerConversation := validIncident(manifest)
	incidentFromNewerConversation.Channel.ID = manifest.Telegram.NewerChatID
	incidentWithoutCoverage := validIncident(manifest)
	incidentWithoutCoverage.Coverage = nil
	for _, test := range []struct {
		name          string
		health        *comisSystemHealthReport
		explanation   *comisIncidentReport
		wantErrorPart string
	}{
		{name: "system source coverage", health: &healthWithoutCoverage, wantErrorPart: "system health source coverage is incomplete"},
		{name: "wrong Telegram origin", explanation: &incidentFromNewerConversation, wantErrorPart: "Telegram origin is invalid"},
		{name: "session source coverage", explanation: &incidentWithoutCoverage, wantErrorPart: "session explanation source coverage is incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			executor := &fixtureExecutor{manifest: manifest}
			var err error
			if test.health != nil {
				executor.systemHealth, err = json.Marshal(test.health)
			} else {
				executor.explanation, err = json.Marshal(test.explanation)
			}
			if err != nil {
				t.Fatal(err)
			}
			_, collectErr := Collect(
				context.Background(), manifest, filepath.Join(t.TempDir(), "evidence"), executor, manifest.EndedAtMs,
			)
			if collectErr == nil || !strings.Contains(collectErr.Error(), test.wantErrorPart) {
				t.Fatalf("Collect() error = %v, want %q", collectErr, test.wantErrorPart)
			}
		})
	}
}

func TestCollectRefusesComisReportsWithoutRequiredCampaignToolOutcomes(t *testing.T) {
	manifest := validManifest()
	explanation := validIncident(manifest)
	explanation.ToolStats = map[string]comisToolCount{"reconcile_task": {OK: 1}}
	explanation.Failures = explanation.Failures[:0]
	encoded, err := json.Marshal(explanation)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fixtureExecutor{manifest: manifest, explanation: encoded}
	_, collectErr := Collect(
		context.Background(), manifest, filepath.Join(t.TempDir(), "evidence"), executor, manifest.EndedAtMs,
	)
	if collectErr == nil || !strings.Contains(collectErr.Error(), "required Comis tool evidence is incomplete") {
		t.Fatalf("Collect() error = %v, want incomplete campaign tool evidence", collectErr)
	}
}

func TestCollectRefusesCleanupCountsWithoutPreconditionFailureEvidence(t *testing.T) {
	manifest := validManifest()
	explanation := validIncident(manifest)
	explanation.Failures = explanation.Failures[:0]
	encoded, err := json.Marshal(explanation)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fixtureExecutor{manifest: manifest, explanation: encoded}
	_, collectErr := Collect(
		context.Background(), manifest, filepath.Join(t.TempDir(), "evidence"), executor, manifest.EndedAtMs,
	)
	if collectErr == nil || !strings.Contains(collectErr.Error(), "cleanup refusal evidence is incomplete") {
		t.Fatalf("Collect() error = %v, want cleanup refusal evidence", collectErr)
	}
}

func TestCollectRefusesCleanupFailuresWithoutBothRequiredReasons(t *testing.T) {
	manifest := validManifest()
	explanation := validIncident(manifest)
	for index := range explanation.Failures {
		explanation.Failures[index].ErrorPreview = "cleanup refused"
	}
	encoded, err := json.Marshal(explanation)
	if err != nil {
		t.Fatal(err)
	}
	executor := &fixtureExecutor{manifest: manifest, explanation: encoded}
	_, collectErr := Collect(
		context.Background(), manifest, filepath.Join(t.TempDir(), "evidence"), executor, manifest.EndedAtMs,
	)
	if collectErr == nil || !strings.Contains(collectErr.Error(), "cleanup refusal reasons are incomplete") {
		t.Fatalf("Collect() error = %v, want distinct cleanup refusal reasons", collectErr)
	}
}

func TestCollectRefusesExistingCampaignEvidenceDirectory(t *testing.T) {
	manifest := validManifest()
	root := filepath.Join(t.TempDir(), "evidence")
	if err := os.MkdirAll(filepath.Join(root, manifest.CampaignID), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Collect(context.Background(), manifest, root, &fixtureExecutor{manifest: manifest}, manifest.EndedAtMs)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected existing-directory refusal, got %v", err)
	}
}

func TestCollectUsesOnlyFixedExecutableAndArgumentCatalog(t *testing.T) {
	manifest := validManifest()
	executor := &fixtureExecutor{manifest: manifest}
	root := filepath.Join(t.TempDir(), "evidence")
	if _, err := Collect(context.Background(), manifest, root, executor, manifest.EndedAtMs); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		manifest.DevCrew.CLIPath: true, manifest.Comis.NodePath: true,
		manifest.GitHub.CLIPath: true, manifest.GitHub.GitPath: true,
	}
	for _, call := range executor.calls {
		if !allowed[call.Path] {
			t.Fatalf("unexpected executable %q", call.Path)
		}
		if reflect.DeepEqual(call.Args, []string{"-c"}) {
			t.Fatalf("shell arguments are forbidden: %#v", call)
		}
	}
}
