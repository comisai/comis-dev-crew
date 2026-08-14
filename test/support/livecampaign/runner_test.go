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
	return executor.fixture.Run(ctx, command)
}

func TestCampaignRunnerRestartsOnlyIsolatedUnitsAndClosesEvidence(t *testing.T) {
	manifest := validManifest()
	executor := &campaignExecutor{fixture: &fixtureExecutor{manifest: manifest}}
	times := []int64{
		manifest.StartedAtMs + 2500,
		manifest.StartedAtMs + 7500,
		manifest.StartedAtMs + 9500,
		manifest.EndedAtMs,
	}
	runner := CampaignRunner{
		Executor: executor, PollInterval: time.Millisecond,
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
		manifest.StartedAtMs + 2500,
		manifest.StartedAtMs + 7500,
		manifest.StartedAtMs + 9500,
		manifest.EndedAtMs,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err := (CampaignRunner{
		Executor: executor, PollInterval: time.Millisecond,
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
