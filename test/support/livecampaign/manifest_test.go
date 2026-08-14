package livecampaign

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validManifest() Manifest {
	return Manifest{
		SchemaVersion: 1,
		CampaignID:    "e0-real-telegram-20260814",
		StartedAtMs:   1_786_656_000_000,
		EndedAtMs:     1_786_659_600_000,
		Source: SourcePins{
			ComisCommit: strings.Repeat("c", 40), DevCrewCommit: strings.Repeat("d", 40),
		},
		Protocol: ProtocolPin{
			ID: "comis.capability-service/1", Digest: "fff96cf5105d9cda9da5dfd2fbc7e9f15242754f63d7f8155cde4ef874d5c52b",
		},
		Artifacts: []ArtifactPin{
			{Kind: "comis-cli", Path: "/opt/comis/packages/cli/dist/cli.js", SHA256: strings.Repeat("1", 64), Version: "1.0.61"},
			{Kind: "devcrew", Path: "/opt/devcrew/bin/devcrew", SHA256: strings.Repeat("2", 64), Version: "dev"},
			{Kind: "devcrew-mcp", Path: "/opt/devcrew/bin/devcrew-mcp", SHA256: strings.Repeat("3", 64), Version: "dev"},
			{Kind: "devcrew-report", Path: "/opt/devcrew/bin/devcrew-report", SHA256: strings.Repeat("4", 64), Version: "dev"},
			{Kind: "devcrew-service", Path: "/opt/devcrew/bin/devcrew-service", SHA256: strings.Repeat("5", 64), Version: "dev"},
		},
		DevCrew: DevCrewTarget{
			CLIPath: "/opt/devcrew/bin/devcrew", SocketPath: "/run/devcrew-e0/service.sock",
			RepositoryID: "comis-repository",
		},
		Comis: ComisTarget{
			NodePath: "/usr/bin/node", CLIScriptPath: "/opt/comis/packages/cli/dist/cli.js",
			CodeRoot: "/opt/comis", DataDir: "/var/lib/comis-e0", AgentID: "devcrew-liaison",
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
			ComisUnit: "comis-e0-live.service",
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
