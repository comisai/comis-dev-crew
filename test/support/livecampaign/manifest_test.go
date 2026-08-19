package livecampaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: 1,
		CampaignID:    "e0-real-telegram-20260814",
		StartedAtMs:   1_786_656_000_000,
		EndedAtMs:     1_786_663_200_000,
		Source: SourcePins{
			ComisCommit: strings.Repeat("c", 40), DevCrewCommit: strings.Repeat("d", 40),
		},
		Protocol: ProtocolPin{
			ID: "comis.capability-service/1", Digest: "86f5f5eb3d8147ccf85200adb475ccfecdbe28f6acdeb5446b8b8a71edfa9b33",
		},
		Artifacts: []ArtifactPin{
			{Kind: "comis-cli", Path: "/opt/comis/packages/cli/dist/cli.js", SHA256: strings.Repeat("1", 64), Version: "1.0.61"},
			{Kind: "devcrew", Path: "/opt/devcrew/bin/devcrew", SHA256: strings.Repeat("2", 64), Version: "dev"},
			{Kind: "devcrew-mcp", Path: "/opt/devcrew/bin/devcrew-mcp", SHA256: strings.Repeat("3", 64), Version: "dev"},
			{Kind: "devcrew-report", Path: "/opt/devcrew/bin/devcrew-report", SHA256: strings.Repeat("4", 64), Version: "dev"},
			{Kind: "devcrew-service", Path: "/opt/devcrew/bin/devcrew-service", SHA256: strings.Repeat("5", 64), Version: "dev"},
		},
		Workers: []WorkerPin{
			{Kind: "codex", ProfileID: "codex-reviewed", Path: "/opt/codex/bin/codex", SHA256: strings.Repeat("6", 64), Version: "codex-cli 0.147.0"},
			{Kind: "claude", ProfileID: "claude-reviewed", Path: "/opt/claude/bin/claude", SHA256: strings.Repeat("7", 64), Version: "2.1.224 (Claude Code)"},
		},
		DevCrew: DevCrewTarget{
			CLIPath: "/opt/devcrew/bin/devcrew", CodeRoot: "/opt/devcrew", SocketPath: "/run/devcrew-e0/service.sock",
			DatabasePath: "/var/lib/devcrew-e0/state.db", WorktreeRoot: "/var/lib/devcrew-e0/worktrees",
			RepositoryID: "comis-repository",
		},
		Comis: ComisTarget{
			NodePath: "/usr/bin/node", CLIScriptPath: "/opt/comis/packages/cli/dist/cli.js",
			CodeRoot: "/opt/comis", DataDir: "/var/lib/comis-e0", DatabasePath: "/var/lib/comis-e0/memory.db",
			AgentID:               "devcrew-liaison",
			SecretResidencyScript: "/opt/comis/test/live/self-driving/scripts/secret-residency.mjs",
			SecretNames:           []string{"TELEGRAM_BOT_TOKEN", "GITHUB_TOKEN"},
			ExplainRefs:           []string{"root-session-e0-origin"},
		},
		Telegram: TelegramTarget{
			OriginChatID: "telegram-chat-origin", NewerChatID: "telegram-chat-newer", SenderID: "user_a",
			Checkpoints: []TelegramCheckpoint{
				{Kind: "task_request", ChatID: "telegram-chat-origin", Marker: "e0cp-task-01"},
				{Kind: "unrelated_conversation", ChatID: "telegram-chat-newer", Marker: "e0cp-newer-01"},
				{Kind: "mcp_restarted_ack", ChatID: "telegram-chat-origin", Marker: "e0cp-mcp-01"},
				{Kind: "decision_reply", ChatID: "telegram-chat-origin", Marker: "e0cp-decision-01"},
				{Kind: "pause_handback", ChatID: "telegram-chat-origin", Marker: "e0cp-handback-01"},
				{Kind: "reconcile_approval", ChatID: "telegram-chat-origin", Marker: "e0cp-reconcile-01"},
				{Kind: "devcrew_restart_ready", ChatID: "telegram-chat-origin", Marker: "e0cp-devcrew-ready-01"},
				{Kind: "devcrew_restarted_ack", ChatID: "telegram-chat-origin", Marker: "e0cp-devcrew-ack-01"},
				{Kind: "comis_restart_ready", ChatID: "telegram-chat-origin", Marker: "e0cp-comis-ready-01"},
				{Kind: "comis_restarted_ack", ChatID: "telegram-chat-origin", Marker: "e0cp-comis-ack-01"},
				{Kind: "cleanup_confirmation", ChatID: "telegram-chat-origin", Marker: "e0cp-cleanup-01"},
			},
		},
		GitHub: GitHubTarget{
			CLIPath: "/usr/bin/gh", GitPath: "/usr/bin/git", Repository: "comisai/comis", PrimaryCheckout: "/srv/comis-primary",
			BaseBranch: "main", RequiredChecks: []string{"validate"},
		},
		Services: ServiceTargets{
			SystemctlPath: "/usr/bin/systemctl", IsolationLabel: "e0-live",
			MCPUnit: "devcrew-e0-live-mcp.service", DevCrewUnit: "devcrew-e0-live.service",
			ComisUnit: "comis-e0-live.service", MCPUnitSHA256: sha256Hex([]byte("mcp unit\n")),
			DevCrewUnitSHA256: sha256Hex([]byte("devcrew unit\n")), ComisUnitSHA256: sha256Hex([]byte("comis unit\n")),
		},
		Recovery: RecoveryTarget{
			CandidateConfigPath:          "/var/lib/devcrew-e0/candidate.json",
			SyntheticComisDataDir:        "/var/lib/devcrew-e0-rollback/comis",
			SyntheticDevCrewDatabasePath: "/var/lib/devcrew-e0-rollback/devcrew.db",
			PreviousDevCrewRelease:       "v0.1.0",
			PreviousArtifacts: []ArtifactPin{
				{Kind: "comis-cli", Path: "/opt/previous/comis/cli.js", SHA256: strings.Repeat("8", 64), Version: "1.0.60"},
				{Kind: "devcrew", Path: "/opt/previous/devcrew/devcrew", SHA256: strings.Repeat("9", 64), Version: "0.0.9"},
				{Kind: "devcrew-mcp", Path: "/opt/previous/devcrew/devcrew-mcp", SHA256: strings.Repeat("a", 64), Version: "0.0.9"},
				{Kind: "devcrew-report", Path: "/opt/previous/devcrew/devcrew-report", SHA256: strings.Repeat("b", 64), Version: "0.0.9"},
				{Kind: "devcrew-service", Path: "/opt/previous/devcrew/devcrew-service", SHA256: strings.Repeat("c", 64), Version: "0.0.9"},
			},
		},
		Tasks: []TaskExpectation{
			{TaskHandle: "task-codex-e0", WorkerProfileID: "codex-reviewed", ManagedRunID: "managed-run-codex", ExpectReconciliation: true},
			{TaskHandle: "task-claude-e0", WorkerProfileID: "claude-reviewed", ManagedRunID: "managed-run-claude"},
		},
		Operations: []OperationExpectation{
			{OperationID: "operation-reconcile-e0", TaskHandle: "task-codex-e0", Command: "ReconcileTask"},
			{OperationID: "operation-handback-e0", TaskHandle: "task-claude-e0", Command: "HandbackTask"},
			{OperationID: "operation-cleanup-codex", TaskHandle: "task-codex-e0", Command: "CleanupTask"},
			{OperationID: "operation-cleanup-claude", TaskHandle: "task-claude-e0", Command: "CleanupTask"},
		},
	}
}

func TestManifestRejectsSourceProtocolAndArtifactIdentityDrift(t *testing.T) {
	manifest := validManifest()
	manifest.Protocol.Digest = strings.Repeat("0", 64)
	if _, err := LoadManifest(writeManifest(t, manifest, "-protocol")); err == nil || !strings.Contains(err.Error(), "compiled protocol") {
		t.Fatalf("expected protocol-drift refusal, got %v", err)
	}

	manifest = validManifest()
	manifest.Source.ComisCommit = "short"
	if _, err := LoadManifest(writeManifest(t, manifest, "-source")); err == nil || !strings.Contains(err.Error(), "source commits") {
		t.Fatalf("expected source-pin refusal, got %v", err)
	}

	manifest = validManifest()
	manifest.Artifacts = manifest.Artifacts[:len(manifest.Artifacts)-1]
	if _, err := LoadManifest(writeManifest(t, manifest, "-artifact")); err == nil || !strings.Contains(err.Error(), "artifact catalog") {
		t.Fatalf("expected incomplete-artifact refusal, got %v", err)
	}
}

func TestManifestRejectsPlaceholderSourceCommits(t *testing.T) {
	manifest := validManifest()
	manifest.Source.DevCrewCommit = strings.Repeat("0", 40)
	if _, err := LoadManifest(writeManifest(t, manifest, "-placeholder-source")); err == nil || !strings.Contains(err.Error(), "source commits") {
		t.Fatalf("expected placeholder-source refusal, got %v", err)
	}
}

func TestManifestRequiresExactPreviousDevCrewReleaseCoordinate(t *testing.T) {
	manifest := validManifest()
	manifest.Recovery.PreviousDevCrewRelease = "dev"
	if _, err := LoadManifest(writeManifest(t, manifest, "-previous-release")); err == nil ||
		!strings.Contains(err.Error(), "previous DevCrew release") {
		t.Fatalf("expected previous-release refusal, got %v", err)
	}
}

func TestManifestAcceptsOperationIdentitiesDeferredUntilTelegramMutationCompletes(t *testing.T) {
	manifest := validManifest()
	for index := range manifest.Operations {
		manifest.Operations[index].OperationID = ""
	}
	if _, err := LoadManifest(writeManifest(t, manifest, "-deferred-operations")); err != nil {
		t.Fatalf("LoadManifest(deferred operation identities) error = %v", err)
	}
}

func TestManifestRequiresWorkerPinsForBothTaskProfiles(t *testing.T) {
	manifest := validManifest()
	manifest.Workers[1].ProfileID = manifest.Workers[0].ProfileID
	if _, err := LoadManifest(writeManifest(t, manifest, "-worker-profile")); err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("expected worker-profile refusal, got %v", err)
	}
	manifest = validManifest()
	manifest.Workers = manifest.Workers[:1]
	if _, err := LoadManifest(writeManifest(t, manifest, "-worker-catalog")); err == nil || !strings.Contains(err.Error(), "worker") {
		t.Fatalf("expected worker-catalog refusal, got %v", err)
	}
}

func writeManifest(t *testing.T, manifest Manifest, suffix string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "campaign"+suffix+".json")
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

func TestManifestAcceptsExactProtectedCampaignContract(t *testing.T) {
	manifest := validManifest()
	loaded, err := LoadManifest(writeManifest(t, manifest, ""))
	if err != nil {
		t.Fatalf("load valid manifest: %v", err)
	}
	if loaded.CampaignID != manifest.CampaignID || len(loaded.Tasks) != 2 {
		t.Fatalf("unexpected loaded manifest: %#v", loaded)
	}
}

func TestManifestRejectsUnknownFieldsWithoutWideningAuthority(t *testing.T) {
	manifest := validManifest()
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	altered := strings.Replace(string(contents), `"schemaVersion":1`, `"schemaVersion":1,"token":"must-not-be-accepted"`, 1)
	path := filepath.Join(t.TempDir(), "campaign.json")
	if err := os.WriteFile(path, []byte(altered), 0o600); err != nil {
		t.Fatalf("write altered manifest: %v", err)
	}
	if _, err := LoadManifest(path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown-field refusal, got %v", err)
	}
}

func TestManifestRequiresEveryHumanAndRestartCheckpoint(t *testing.T) {
	manifest := validManifest()
	manifest.Telegram.Checkpoints = manifest.Telegram.Checkpoints[:len(manifest.Telegram.Checkpoints)-1]
	if _, err := LoadManifest(writeManifest(t, manifest, "-missing")); err == nil || !strings.Contains(err.Error(), "cleanup_confirmation") {
		t.Fatalf("expected missing-checkpoint refusal, got %v", err)
	}
}

func TestManifestRequiresDistinctTwoLaneRecoveryAndHandbackEvidence(t *testing.T) {
	manifest := validManifest()
	manifest.Tasks[1].WorkerProfileID = manifest.Tasks[0].WorkerProfileID
	if _, err := LoadManifest(writeManifest(t, manifest, "-profiles")); err == nil || !strings.Contains(err.Error(), "worker profiles") {
		t.Fatalf("expected duplicate-profile refusal, got %v", err)
	}

	manifest = validManifest()
	manifest.Operations = manifest.Operations[1:]
	if _, err := LoadManifest(writeManifest(t, manifest, "-operations")); err == nil || !strings.Contains(err.Error(), "ReconcileTask") {
		t.Fatalf("expected recovery-operation refusal, got %v", err)
	}
}

func TestManifestConfinesServiceRestartsToIsolationLabel(t *testing.T) {
	manifest := validManifest()
	manifest.Services.ComisUnit = "comis.service"
	if _, err := LoadManifest(writeManifest(t, manifest, "-unit")); err == nil || !strings.Contains(err.Error(), "isolation label") {
		t.Fatalf("expected protected-unit refusal, got %v", err)
	}
}

func TestManifestRequiresExactServiceUnitDigests(t *testing.T) {
	manifest := validManifest()
	manifest.Services.ComisUnitSHA256 = "untrusted"
	if _, err := LoadManifest(writeManifest(t, manifest, "-unit-digest")); err == nil || !strings.Contains(err.Error(), "unit definition") {
		t.Fatalf("expected service-unit-digest refusal, got %v", err)
	}
}

// campaignKindJSON rewrites the manifest's campaign-kind discriminator, inserting it when the
// encoded manifest does not already carry one, so one helper serves every kind under test.
func campaignKindJSON(kind string) func(string) string {
	return func(contents string) string {
		if strings.Contains(contents, `"campaignKind":`) {
			return campaignKindValuePattern.ReplaceAllString(contents, `"campaignKind":"`+kind+`"`)
		}
		return strings.Replace(contents, `"schemaVersion":1`, `"schemaVersion":1,"campaignKind":"`+kind+`"`, 1)
	}
}

func withoutCampaignKind(contents string) string {
	return campaignKindFieldPattern.ReplaceAllString(contents, "")
}

var (
	// The value pattern rewrites a kind in place; the field pattern removes the whole
	// member, trailing separator included, so the result stays well-formed JSON.
	campaignKindValuePattern = regexp.MustCompile(`"campaignKind":"[^"]*"`)
	campaignKindFieldPattern = regexp.MustCompile(`"campaignKind":"[^"]*",?`)
)

func writeManifestJSON(t *testing.T, manifest Manifest, suffix string, edit func(string) string) string {
	t.Helper()
	contents, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	path := filepath.Join(t.TempDir(), "campaign"+suffix+".json")
	if err := os.WriteFile(path, []byte(edit(string(contents))), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return path
}

// A campaign declares only the checkpoint arc its channel can actually drive. The eleven
// e0cp markers are sent by a human from the Telegram app, so a campaign driven by the
// loopback emulator has no sender that could ever satisfy them.
func TestManifestBindsCheckpointDeclarabilityToCampaignKind(t *testing.T) {
	realTelegram := validManifest()
	if _, err := LoadManifest(writeManifestJSON(t, realTelegram, "-real", campaignKindJSON("real_telegram"))); err != nil {
		t.Fatalf("LoadManifest(real_telegram campaign) error = %v", err)
	}

	emulator := validManifest()
	emulator.Telegram.Checkpoints = nil
	if _, err := LoadManifest(writeManifestJSON(t, emulator, "-emulator", campaignKindJSON("emulator"))); err != nil {
		t.Fatalf("LoadManifest(emulator campaign without the human arc) error = %v", err)
	}

	declared := validManifest()
	if _, err := LoadManifest(writeManifestJSON(t, declared, "-emulator-declared", campaignKindJSON("emulator"))); err == nil ||
		!strings.Contains(err.Error(), "emulator") {
		t.Fatalf("expected emulator checkpoint-declaration refusal, got %v", err)
	}

	missingArc := validManifest()
	missingArc.Telegram.Checkpoints = nil
	if _, err := LoadManifest(writeManifestJSON(t, missingArc, "-real-missing", campaignKindJSON("real_telegram"))); err == nil ||
		!strings.Contains(err.Error(), "task_request") {
		t.Fatalf("expected real_telegram missing-checkpoint refusal, got %v", err)
	}

	if _, err := LoadManifest(writeManifestJSON(t, validManifest(), "-unknown-kind", campaignKindJSON("paper"))); err == nil ||
		!strings.Contains(err.Error(), "campaignKind") {
		t.Fatalf("expected unknown campaign-kind refusal, got %v", err)
	}

	if _, err := LoadManifest(writeManifestJSON(t, validManifest(), "-absent-kind", withoutCampaignKind)); err == nil ||
		!strings.Contains(err.Error(), "campaignKind") {
		t.Fatalf("expected absent campaign-kind refusal, got %v", err)
	}
}
