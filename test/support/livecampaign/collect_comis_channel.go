package livecampaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

func (instance *collector) collectTelegramAndComis() error {
	comisEnv := instance.comisEnv()
	messageArgs := []string{
		instance.manifest.Comis.CLIScriptPath, "messages", "--channel", "telegram",
		"--sender", instance.manifest.Telegram.SenderID, "--agent", instance.manifest.Comis.AgentID,
		"--since", strconv.FormatInt(instance.manifest.StartedAtMs, 10),
		"--until", strconv.FormatInt(instance.manifest.EndedAtMs+1, 10),
		"--limit", "10000", "--format", "json",
	}
	var report MessageReport
	if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: messageArgs, Env: comisEnv}, &report); err != nil {
		return fmt.Errorf("collect live closeout: Telegram messages unavailable: %w", err)
	}
	checkpoints := []CheckpointEvidence{}
	if instance.manifest.CampaignKind == CampaignKindRealTelegram {
		verified, err := VerifyMessages(instance.manifest, report)
		if err != nil {
			return fmt.Errorf("collect live closeout: Telegram evidence refused: %w", err)
		}
		if len(verified) != len(requiredCheckpointKinds) {
			return errors.New("collect live closeout: Telegram checkpoint evidence is incomplete")
		}
		checkpoints = verified
	}
	if err := instance.writeJSON("telegram-checkpoints.json", checkpoints); err != nil {
		return err
	}
	durationHours := (instance.manifest.EndedAtMs - instance.manifest.StartedAtMs + 3_599_999) / 3_600_000
	if durationHours < 1 {
		durationHours = 1
	}
	var healthJSON json.RawMessage
	if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: []string{
		instance.manifest.Comis.CLIScriptPath, "system-health", "--since", strconv.FormatInt(durationHours, 10),
		"--format", "json", "--offline",
	}, Env: comisEnv}, &healthJSON); err != nil {
		return fmt.Errorf("collect live closeout: Comis system health unavailable: %w", err)
	}
	var health comisSystemHealthReport
	if err := json.Unmarshal(healthJSON, &health); err != nil {
		return errors.New("collect live closeout: Comis system health is malformed")
	}
	if err := verifyComisSystemHealth(instance.manifest, health); err != nil {
		return fmt.Errorf("collect live closeout: system health evidence refused: %w", err)
	}
	if err := instance.writeRawJSON("comis-system-health.json", healthJSON); err != nil {
		return err
	}
	explanations := make([]comisIncidentReport, 0, len(instance.manifest.Comis.ExplainRefs))
	for index, reference := range instance.manifest.Comis.ExplainRefs {
		var explanationJSON json.RawMessage
		if err := instance.runJSON(Command{Path: instance.manifest.Comis.NodePath, Args: []string{
			instance.manifest.Comis.CLIScriptPath, "explain", reference, "--format", "json", "--depth", "full", "--offline",
		}, Env: comisEnv}, &explanationJSON); err != nil {
			return fmt.Errorf("collect live closeout: Comis explanation %d unavailable: %w", index+1, err)
		}
		var explanation comisIncidentReport
		if err := json.Unmarshal(explanationJSON, &explanation); err != nil {
			return fmt.Errorf("collect live closeout: Comis explanation %d is malformed", index+1)
		}
		if err := verifyComisIncident(instance.manifest, explanation); err != nil {
			return fmt.Errorf("collect live closeout: Comis explanation %d evidence refused: %w", index+1, err)
		}
		if err := instance.writeRawJSON(fmt.Sprintf("comis-explain-%02d.json", index+1), explanationJSON); err != nil {
			return err
		}
		explanations = append(explanations, explanation)
	}
	if err := verifyComisCampaignTools(explanations); err != nil {
		return fmt.Errorf("collect live closeout: %w", err)
	}
	if instance.manifest.CampaignKind == CampaignKindRealTelegram {
		instance.pass("real_human_telegram_checkpoints")
	} else {
		instance.record("real_human_telegram_checkpoints", evidenceStatusNotClaimed)
	}
	instance.pass("comis_observability")
	instance.pass("comis_campaign_tool_outcomes")
	return nil
}
