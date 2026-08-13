package livecampaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type CampaignRunner struct {
	Executor     Executor
	PollInterval time.Duration
	NowMs        func() int64
	Logf         func(string, ...any)
}

func (runner CampaignRunner) Run(ctx context.Context, manifest Manifest, evidenceRoot string) (Verdict, error) {
	if ctx == nil || runner.Executor == nil || runner.NowMs == nil || runner.PollInterval <= 0 {
		return Verdict{}, errors.New("run protected live campaign: runner dependencies are required")
	}
	if err := manifest.validate(); err != nil {
		return Verdict{}, fmt.Errorf("run protected live campaign: %w", err)
	}
	runner.log("waiting for the human Telegram task request")
	if _, err := runner.waitCheckpoint(ctx, manifest, "task_request", manifest.StartedAtMs); err != nil {
		return Verdict{}, err
	}
	runner.log("waiting for two independently working DevCrew lanes")
	if err := runner.wait(ctx, "worker overlap", func() (bool, error) {
		fleet, err := runner.readFleet(ctx, manifest)
		if err != nil {
			return false, nil
		}
		return fleetHasExactTaskState(manifest, fleet, domain.TaskWorking), nil
	}); err != nil {
		return Verdict{}, err
	}
	mcpRestartedAt := runner.NowMs()
	if err := runner.restartUnit(ctx, manifest, manifest.Services.MCPUnit); err != nil {
		return Verdict{}, err
	}
	runner.log("DevCrew MCP facade replaced; waiting for liaison acknowledgement")
	if _, err := runner.waitCheckpoint(ctx, manifest, "mcp_restarted_ack", mcpRestartedAt); err != nil {
		return Verdict{}, err
	}
	for _, kind := range []string{"decision_reply", "pause_handback", "reconcile_approval"} {
		runner.log("waiting for Telegram checkpoint %s", kind)
		if _, err := runner.waitCheckpoint(ctx, manifest, kind, manifest.StartedAtMs); err != nil {
			return Verdict{}, err
		}
	}
	if _, err := runner.waitCheckpoint(ctx, manifest, "devcrew_restart_ready", manifest.StartedAtMs); err != nil {
		return Verdict{}, err
	}
	devCrewRestartedAt := runner.NowMs()
	if err := runner.restartUnit(ctx, manifest, manifest.Services.DevCrewUnit); err != nil {
		return Verdict{}, err
	}
	if err := runner.waitDevCrewHealthy(ctx, manifest); err != nil {
		return Verdict{}, err
	}
	runner.log("DevCrew restarted with durable task identities; waiting for human acknowledgement")
	if _, err := runner.waitCheckpoint(ctx, manifest, "devcrew_restarted_ack", devCrewRestartedAt); err != nil {
		return Verdict{}, err
	}
	if _, err := runner.waitCheckpoint(ctx, manifest, "comis_restart_ready", manifest.StartedAtMs); err != nil {
		return Verdict{}, err
	}
	comisRestartedAt := runner.NowMs()
	if err := runner.restartUnit(ctx, manifest, manifest.Services.ComisUnit); err != nil {
		return Verdict{}, err
	}
	runner.log("Comis restarted; waiting for a new human-origin Telegram acknowledgement")
	if _, err := runner.waitCheckpoint(ctx, manifest, "comis_restarted_ack", comisRestartedAt); err != nil {
		return Verdict{}, err
	}
	if _, err := runner.waitCheckpoint(ctx, manifest, "cleanup_confirmation", manifest.StartedAtMs); err != nil {
		return Verdict{}, err
	}
	runner.log("waiting for both task cleanups to complete")
	if err := runner.wait(ctx, "final cleanup", func() (bool, error) {
		fleet, err := runner.readFleet(ctx, manifest)
		if err != nil {
			return false, nil
		}
		return fleetHasExactTaskState(manifest, fleet, domain.TaskCleaned), nil
	}); err != nil {
		return Verdict{}, err
	}
	runner.log("campaign state is terminal; collecting bounded closeout evidence")
	return Collect(ctx, manifest, evidenceRoot, runner.Executor, runner.NowMs())
}

func (runner CampaignRunner) restartUnit(ctx context.Context, manifest Manifest, unit string) error {
	if _, err := runner.Executor.Run(ctx, Command{
		Path: manifest.Services.SystemctlPath, Args: []string{"restart", unit},
	}); err != nil {
		return fmt.Errorf("run protected live campaign: restart isolated unit %s: %w", unit, err)
	}
	return runner.wait(ctx, "isolated unit activation", func() (bool, error) {
		_, err := runner.Executor.Run(ctx, Command{
			Path: manifest.Services.SystemctlPath, Args: []string{"is-active", "--quiet", unit},
		})
		return err == nil, nil
	})
}

func (runner CampaignRunner) waitDevCrewHealthy(ctx context.Context, manifest Manifest) error {
	return runner.wait(ctx, "DevCrew health after restart", func() (bool, error) {
		base := []string{"--socket", manifest.DevCrew.SocketPath, "doctor", "--format", "json"}
		output, err := runner.Executor.Run(ctx, Command{Path: manifest.DevCrew.CLIPath, Args: base})
		if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes {
			return false, nil
		}
		var report application.DiagnosticReport
		if json.Unmarshal(output, &report) != nil {
			return false, nil
		}
		return report.SchemaVersion == 1 && report.Completeness == application.CompletenessComplete &&
			report.ServiceHealth == application.HealthHealthy && report.ComisHealth == application.HealthHealthy, nil
	})
}

func (runner CampaignRunner) waitCheckpoint(
	ctx context.Context,
	manifest Manifest,
	kind string,
	minimumEpochMs int64,
) (CheckpointEvidence, error) {
	var evidence CheckpointEvidence
	err := runner.wait(ctx, "Telegram checkpoint "+kind, func() (bool, error) {
		report, err := runner.readMessages(ctx, manifest)
		if err != nil {
			return false, nil
		}
		checkpoint, found, err := findCheckpoint(manifest, report, kind, minimumEpochMs)
		if err != nil {
			return false, err
		}
		if found {
			evidence = checkpoint
		}
		return found, nil
	})
	return evidence, err
}

func (runner CampaignRunner) readMessages(ctx context.Context, manifest Manifest) (MessageReport, error) {
	args := []string{
		manifest.Comis.CLIScriptPath, "messages", "--channel", "telegram",
		"--sender", manifest.Telegram.SenderID, "--agent", manifest.Comis.AgentID,
		"--since", strconv.FormatInt(manifest.StartedAtMs, 10),
		"--until", strconv.FormatInt(manifest.EndedAtMs+1, 10),
		"--limit", "10000", "--format", "json",
	}
	output, err := runner.Executor.Run(ctx, Command{
		Path: manifest.Comis.NodePath, Args: args,
		Env: map[string]string{
			"COMIS_DATA_DIR":     manifest.Comis.DataDir,
			"COMIS_CONFIG_PATHS": filepath.Join(manifest.Comis.DataDir, "config.yaml"),
		},
	})
	if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes {
		return MessageReport{}, errors.New("read Telegram checkpoints: message report unavailable")
	}
	var report MessageReport
	if err := json.Unmarshal(output, &report); err != nil {
		return MessageReport{}, errors.New("read Telegram checkpoints: message report is malformed")
	}
	if report.Schema != "comis-offline-channel-messages-report" || report.SchemaVersion != 2 || !report.Completeness.Complete {
		return MessageReport{}, errors.New("read Telegram checkpoints: message report is incomplete")
	}
	return report, nil
}

func (runner CampaignRunner) readFleet(ctx context.Context, manifest Manifest) (application.FleetSnapshot, error) {
	output, err := runner.Executor.Run(ctx, Command{
		Path: manifest.DevCrew.CLIPath,
		Args: []string{"--socket", manifest.DevCrew.SocketPath, "status", "--format", "json"},
	})
	if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes {
		return application.FleetSnapshot{}, errors.New("read DevCrew fleet: report unavailable")
	}
	var fleet application.FleetSnapshot
	if err := json.Unmarshal(output, &fleet); err != nil {
		return application.FleetSnapshot{}, errors.New("read DevCrew fleet: report is malformed")
	}
	if fleet.SchemaVersion != 1 || fleet.Completeness != application.CompletenessComplete ||
		fleet.ServiceHealth != application.HealthHealthy || fleet.ComisHealth != application.HealthHealthy {
		return application.FleetSnapshot{}, errors.New("read DevCrew fleet: report is incomplete or degraded")
	}
	return fleet, nil
}

func (runner CampaignRunner) wait(ctx context.Context, stage string, predicate func() (bool, error)) error {
	for {
		ready, err := predicate()
		if err != nil {
			return fmt.Errorf("run protected live campaign: %s: %w", stage, err)
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(runner.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("run protected live campaign: wait for %s: %w", stage, ctx.Err())
		case <-timer.C:
		}
	}
}

func (runner CampaignRunner) log(format string, arguments ...any) {
	if runner.Logf != nil {
		runner.Logf(format, arguments...)
	}
}

func findCheckpoint(
	manifest Manifest,
	report MessageReport,
	kind string,
	minimumEpochMs int64,
) (CheckpointEvidence, bool, error) {
	var expected TelegramCheckpoint
	foundExpectation := false
	for _, checkpoint := range manifest.Telegram.Checkpoints {
		if checkpoint.Kind == kind {
			expected = checkpoint
			foundExpectation = true
			break
		}
	}
	if !foundExpectation {
		return CheckpointEvidence{}, false, errors.New("checkpoint kind is outside the manifest")
	}
	matches := make([]ChannelMessage, 0, 1)
	for _, message := range report.Messages {
		if strings.Contains(message.Text, expected.Marker) {
			matches = append(matches, message)
		}
	}
	if len(matches) == 0 {
		return CheckpointEvidence{}, false, nil
	}
	if len(matches) != 1 {
		return CheckpointEvidence{}, false, errors.New("checkpoint marker is ambiguous")
	}
	message := matches[0]
	if message.EpochMs < minimumEpochMs {
		return CheckpointEvidence{}, false, nil
	}
	if message.EpochMs > manifest.EndedAtMs || message.ChannelType != "telegram" || message.Origin != "user" ||
		message.SenderID != manifest.Telegram.SenderID || message.ChatID != expected.ChatID ||
		message.AgentID != manifest.Comis.AgentID || message.SessionKey == "" {
		return CheckpointEvidence{}, false, errors.New("checkpoint message has invalid human or conversation authority")
	}
	messageID := ""
	if message.MessageID != nil {
		messageID = *message.MessageID
	}
	return CheckpointEvidence{
		Kind: kind, ChatID: message.ChatID, SenderID: message.SenderID, EpochMs: message.EpochMs,
		MessageID: messageID, SessionKey: message.SessionKey,
	}, true, nil
}

func fleetHasExactTaskState(manifest Manifest, fleet application.FleetSnapshot, state domain.TaskState) bool {
	if len(fleet.Tasks) != len(manifest.Tasks) {
		return false
	}
	for _, expectation := range manifest.Tasks {
		found := false
		for _, task := range fleet.Tasks {
			if task.TaskHandle == expectation.TaskHandle && task.RepositoryID == manifest.DevCrew.RepositoryID &&
				task.WorkerProfileID == expectation.WorkerProfileID && task.State == state {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
