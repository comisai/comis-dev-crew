package livecampaign

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type campaignExecutor struct {
	fixture        *fixtureExecutor
	restartedUnits []string
	mcpRestarted   bool
	comisRestarted bool
	handbackRead   bool
}

func (executor *campaignExecutor) Run(ctx context.Context, command Command) ([]byte, error) {
	manifest := executor.fixture.manifest
	args := strings.Join(command.Args, " ")
	if command.Path == manifest.Services.SystemctlPath {
		if len(command.Args) == 2 && command.Args[0] == "restart" {
			executor.restartedUnits = append(executor.restartedUnits, command.Args[1])
			if command.Args[1] == manifest.Services.MCPUnit {
				executor.mcpRestarted = true
			}
			if command.Args[1] == manifest.Services.ComisUnit {
				executor.comisRestarted = true
			}
			return nil, nil
		}
		if len(command.Args) == 3 && command.Args[0] == "is-active" && command.Args[1] == "--quiet" {
			return nil, nil
		}
	}
	if command.Path == manifest.DevCrew.CLIPath && strings.HasSuffix(args, "status --format json") && !executor.mcpRestarted {
		tasks := make([]application.TaskSummary, 0, len(manifest.Tasks))
		for _, expectation := range manifest.Tasks {
			task := acceptedTask(expectation).Summary
			task.State = domain.TaskWorking
			tasks = append(tasks, task)
		}
		return json.Marshal(application.FleetSnapshot{
			SchemaVersion: 1, CapturedAtMs: manifest.StartedAtMs + 2000, StateVersion: 10,
			Completeness: application.CompletenessComplete, ServiceHealth: application.HealthHealthy,
			ComisHealth: application.HealthHealthy, Tasks: tasks,
		})
	}
	if command.Path == manifest.DevCrew.CLIPath && strings.Contains(args, "task operation "+expectedOperationID(manifest, manifest.Tasks[1].TaskHandle, "HandbackTask")) {
		executor.handbackRead = true
	}
	if command.Path == manifest.DevCrew.CLIPath && strings.HasSuffix(args, "status --format json") && executor.handbackRead {
		executor.handbackRead = false
		working := acceptedTask(manifest.Tasks[0]).Summary
		working.State = domain.TaskWorking
		handback := acceptedTask(manifest.Tasks[1]).Summary
		return json.Marshal(application.FleetSnapshot{
			SchemaVersion: 1, CapturedAtMs: manifest.StartedAtMs + 8000, StateVersion: 15,
			Completeness: application.CompletenessComplete, ServiceHealth: application.HealthHealthy,
			ComisHealth: application.HealthHealthy, Tasks: []application.TaskSummary{working, handback},
		})
	}
	return executor.fixture.Run(ctx, command)
}

func TestCampaignRunnerRestartsOnlyIsolatedUnitsAndClosesEvidence(t *testing.T) {
	manifest := validManifest()
	executor := &campaignExecutor{fixture: &fixtureExecutor{manifest: manifest}}
	times := []int64{
		manifest.StartedAtMs + 2000,
		manifest.StartedAtMs + 2500,
		manifest.StartedAtMs + 7500,
		manifest.StartedAtMs + 9500,
		manifest.EndedAtMs,
	}
	runner := CampaignRunner{
		Executor: executor, PollInterval: time.Millisecond,
		CaptureResources: resourceCaptureFixture(manifest),
		VerifyRecovery:   recoveryEvidenceFixture(manifest),
		NowMs: func() int64 {
			if len(times) == 0 {
				return manifest.EndedAtMs
			}
			value := times[0]
			times = times[1:]
			return value
		},
		Logf: func(string, ...any) {},
	}
	verdict, err := runner.Run(context.Background(), manifest, filepath.Join(t.TempDir(), "evidence"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !verdict.Passed {
		t.Fatalf("verdict = %#v", verdict)
	}
	wantUnits := []string{manifest.Services.MCPUnit, manifest.Services.DevCrewUnit, manifest.Services.ComisUnit}
	if strings.Join(executor.restartedUnits, ",") != strings.Join(wantUnits, ",") {
		t.Fatalf("restarted units = %#v, want %#v", executor.restartedUnits, wantUnits)
	}
}

func TestCampaignRunnerRefusesCampaignWithoutObservedOverlap(t *testing.T) {
	manifest := validManifest()
	executor := &fixtureExecutor{manifest: manifest}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (CampaignRunner{
		Executor: executor, PollInterval: time.Millisecond, NowMs: func() int64 { return manifest.EndedAtMs },
		CaptureResources: resourceCaptureFixture(manifest),
		VerifyRecovery:   recoveryEvidenceFixture(manifest),
	}).Run(ctx, manifest, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || (!errors.Is(err, context.DeadlineExceeded) && !strings.Contains(err.Error(), "overlap")) {
		t.Fatalf("expected overlap refusal, got %v", err)
	}
}

func TestCampaignRunnerRefusesHandbackCompletedBeforeHumanCheckpoint(t *testing.T) {
	manifest := validManifest()
	handbackOperationID := expectedOperationID(manifest, manifest.Tasks[1].TaskHandle, "HandbackTask")
	fixture := &fixtureExecutor{
		manifest: manifest,
		operationUpdatedAt: map[string]int64{
			handbackOperationID: manifest.StartedAtMs + 1000,
		},
	}
	executor := &campaignExecutor{fixture: fixture}
	times := []int64{
		manifest.StartedAtMs + 2000,
		manifest.StartedAtMs + 2500,
		manifest.StartedAtMs + 7500,
		manifest.StartedAtMs + 9500,
		manifest.EndedAtMs,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (CampaignRunner{
		Executor: executor, PollInterval: time.Millisecond,
		CaptureResources: resourceCaptureFixture(manifest),
		VerifyRecovery:   recoveryEvidenceFixture(manifest),
		NowMs: func() int64 {
			if len(times) == 0 {
				return manifest.EndedAtMs
			}
			value := times[0]
			times = times[1:]
			return value
		},
	}).Run(ctx, manifest, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "handback operation after Telegram checkpoint") {
		t.Fatalf("Run(early handback) error = %v", err)
	}
}

func recoveryEvidenceFixture(manifest Manifest) func(context.Context, Manifest, Executor, int64) (RecoveryEvidence, error) {
	return func(context.Context, Manifest, Executor, int64) (RecoveryEvidence, error) {
		return validRecoveryEvidence(manifest), nil
	}
}

func resourceCaptureFixture(manifest Manifest) func(context.Context, Manifest, Executor, int64) (ResourceSnapshot, error) {
	calls := 0
	return func(_ context.Context, _ Manifest, _ Executor, capturedAtMs int64) (ResourceSnapshot, error) {
		observation := validResourceObservation(manifest)
		snapshot := observation.Started
		if calls > 0 {
			snapshot = observation.Finished
		}
		calls++
		snapshot.CapturedAtMs = capturedAtMs
		return snapshot, nil
	}
}

func TestRequireHandbackSiblingWorkingRejectsStoppedSibling(t *testing.T) {
	manifest := validManifest()
	executor := &fixtureExecutor{manifest: manifest}
	runner := CampaignRunner{Executor: executor}
	if err := runner.requireHandbackSiblingWorking(context.Background(), manifest); err == nil ||
		!strings.Contains(err.Error(), "sibling") {
		t.Fatalf("expected stopped-sibling refusal, got %v", err)
	}
}

func TestResolveOperationIdentityUsesDurableTaskEvidenceForEachMutation(t *testing.T) {
	manifest := validManifest()
	detail := acceptedTask(manifest.Tasks[0])
	detail.Evidence.Candidate.ReconciliationOperationID = "operation-reconcile-observed"
	detail.Evidence.Activity.ReportKind = domain.ReportCandidateComplete
	detail.Evidence.Activity.ReportID = "operation-handback-observed"
	detail.Evidence.Cleanup.OperationID = "operation-cleanup-observed"
	executor := fixedOutputExecutor{output: mustMarshalJSON(t, detail)}
	runner := CampaignRunner{Executor: &executor}
	for command, expected := range map[string]string{
		"ReconcileTask": "operation-reconcile-observed",
		"HandbackTask":  "operation-handback-observed",
		"CleanupTask":   "operation-cleanup-observed",
	} {
		operationID, err := runner.resolveOperationIdentity(context.Background(), manifest, detail.Summary.TaskHandle, command)
		if err != nil || operationID != expected {
			t.Fatalf("resolveOperationIdentity(%s) = %q, %v", command, operationID, err)
		}
	}
}

func TestBindTaskOperationIdentityRefusesDriftAndDuplicateAuthority(t *testing.T) {
	manifest := validManifest()
	for index := range manifest.Operations {
		manifest.Operations[index].OperationID = ""
	}
	first := manifest.Operations[0]
	if err := bindTaskOperationIdentity(&manifest, first.TaskHandle, first.Command, "operation-observed-1"); err != nil {
		t.Fatalf("bindTaskOperationIdentity() error = %v", err)
	}
	if err := bindTaskOperationIdentity(&manifest, first.TaskHandle, first.Command, "operation-drifted"); err == nil {
		t.Fatal("bindTaskOperationIdentity(drift) error = nil")
	}
	second := manifest.Operations[1]
	if err := bindTaskOperationIdentity(&manifest, second.TaskHandle, second.Command, "operation-observed-1"); err == nil {
		t.Fatal("bindTaskOperationIdentity(duplicate) error = nil")
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	contents, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

// The protected runner drives the human checkpoint arc, so it refuses an emulator
// campaign at entry rather than stalling on a checkpoint that was never declared.
func TestCampaignRunnerRefusesAnEmulatorCampaignAtEntry(t *testing.T) {
	manifest := validManifest()
	manifest.CampaignKind = CampaignKindEmulator
	manifest.Telegram.Checkpoints = nil
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := (CampaignRunner{
		Executor: &fixtureExecutor{manifest: manifest}, PollInterval: time.Millisecond,
		NowMs:            func() int64 { return manifest.EndedAtMs },
		CaptureResources: resourceCaptureFixture(manifest),
		VerifyRecovery:   recoveryEvidenceFixture(manifest),
	}).Run(ctx, manifest, filepath.Join(t.TempDir(), "evidence"))
	if err == nil || !strings.Contains(err.Error(), "emulator") {
		t.Fatalf("expected emulator campaign refusal, got %v", err)
	}
}
