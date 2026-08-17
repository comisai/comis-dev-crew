package livecampaign

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recoveryExecutorFixture struct {
	manifest Manifest
	calls    []Command
	// unavailableResidencyRuns rejects that many leading secret-residency invocations the way a
	// restarted service does before its authenticated RPC accepts requests.
	unavailableResidencyRuns int
	// malformedResidencyRuns returns that many leading truncated payloads after the rejections.
	malformedResidencyRuns int
	// residencyReport overrides the successful count-only report payload.
	residencyReport string
	residencyRuns   int
}

func (executor *recoveryExecutorFixture) Run(_ context.Context, command Command) ([]byte, error) {
	executor.calls = append(executor.calls, command)
	manifest := executor.manifest
	if command.Path == manifest.Services.SystemctlPath && len(command.Args) == 2 {
		switch command.Args[0] {
		case "stop", "start":
			return nil, nil
		case "cat":
			return []byte("unit=" + command.Args[1] + "\n"), nil
		}
	}
	if command.Path == manifest.Comis.NodePath && len(command.Args) > 0 &&
		command.Args[0] == manifest.Comis.SecretResidencyScript {
		executor.residencyRuns++
		if executor.unavailableResidencyRuns > 0 {
			executor.unavailableResidencyRuns--
			return nil, errors.New("execute protected command: process exited with status 1")
		}
		if executor.malformedResidencyRuns > 0 {
			executor.malformedResidencyRuns--
			return []byte(`{"schemaVersion":1,"scannedFiles":`), nil
		}
		if executor.residencyReport != "" {
			return []byte(executor.residencyReport), nil
		}
		return []byte(`{"schemaVersion":1,"scannedFiles":8,"readErrors":[],"totalMatches":0,"secrets":{"TELEGRAM_BOT_TOKEN":{"retrieved":true,"totalMatches":0},"GITHUB_TOKEN":{"retrieved":true,"totalMatches":0}}}`), nil
	}
	if command.Path == manifest.Comis.NodePath && len(command.Args) >= 3 &&
		command.Args[0] == manifest.Comis.CLIScriptPath && command.Args[1] == "config" && command.Args[2] == "validate" {
		return []byte("Configuration valid\n"), nil
	}
	for _, artifact := range manifest.Recovery.PreviousArtifacts {
		if artifact.Kind == "comis-cli" && command.Path == manifest.Comis.NodePath &&
			len(command.Args) == 2 && command.Args[0] == artifact.Path && command.Args[1] == "--version" {
			return []byte(artifact.Version + "\n"), nil
		}
		if artifact.Kind != "comis-cli" && command.Path == artifact.Path &&
			len(command.Args) == 1 && command.Args[0] == "--version" {
			return []byte(artifact.Kind + " " + artifact.Version + "\n"), nil
		}
		if artifact.Kind == "comis-cli" && command.Path == manifest.Comis.NodePath && len(command.Args) >= 3 &&
			command.Args[0] == artifact.Path && command.Args[1] == "config" && command.Args[2] == "validate" {
			return []byte("Configuration valid\n"), nil
		}
	}
	return nil, errors.New("unexpected recovery command")
}

type rollbackProbeFixture struct {
	called bool
}

func (probe *rollbackProbeFixture) Run(
	_ context.Context,
	servicePath string,
	cliPath string,
	databasePath string,
	_ string,
) error {
	if servicePath == "" || cliPath == "" || databasePath == "" {
		return errors.New("incomplete rollback probe")
	}
	probe.called = true
	return nil
}

func TestCreateAndRestoreRecoveryBackupPreservesStateWithoutPlaintextEnvironment(t *testing.T) {
	manifest := recoveryManifestFixture(t)
	executor := &recoveryExecutorFixture{manifest: manifest}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(parent, "backup")
	backup, err := CreateRecoveryBackup(context.Background(), manifest, backupRoot, executor, manifest.EndedAtMs)
	if err != nil {
		t.Fatalf("CreateRecoveryBackup() error = %v", err)
	}
	if !backup.Passed || backup.Files < 6 || backup.Bytes <= 0 || len(backup.SHA256) != 64 ||
		!backup.PlaintextEnvironmentExcluded || !backup.SecretResidencyPassed {
		t.Fatalf("backup evidence = %#v", backup)
	}
	if _, err := os.Lstat(filepath.Join(backupRoot, "comis-data", ".env")); !os.IsNotExist(err) {
		t.Fatalf("plaintext environment was retained: %v", err)
	}
	restoreRoot := filepath.Join(parent, "restore")
	restore, err := RestoreRecoveryBackup(context.Background(), manifest, backupRoot, restoreRoot, executor, manifest.EndedAtMs)
	if err != nil {
		t.Fatalf("RestoreRecoveryBackup() error = %v", err)
	}
	if !restore.Passed || restore.SHA256 != backup.SHA256 || restore.SQLiteFiles < 3 ||
		!restore.ConfigValidated || !restore.RepositoryRegistryRestored {
		t.Fatalf("restore evidence = %#v", restore)
	}
	wantLifecycle := "stop " + manifest.Services.MCPUnit + ",stop " + manifest.Services.DevCrewUnit +
		",stop " + manifest.Services.ComisUnit + ",start " + manifest.Services.ComisUnit +
		",start " + manifest.Services.DevCrewUnit + ",start " + manifest.Services.MCPUnit
	var lifecycle []string
	for _, call := range executor.calls {
		if call.Path == manifest.Services.SystemctlPath && len(call.Args) == 2 &&
			(call.Args[0] == "stop" || call.Args[0] == "start") {
			lifecycle = append(lifecycle, strings.Join(call.Args, " "))
		}
	}
	if strings.Join(lifecycle, ",") != wantLifecycle {
		t.Fatalf("service lifecycle = %q, want %q", strings.Join(lifecycle, ","), wantLifecycle)
	}
	probe := &rollbackProbeFixture{}
	rollback, err := VerifyRollback(
		context.Background(), manifest, backupRoot, executor, probe.Run, manifest.EndedAtMs,
	)
	if err != nil {
		t.Fatalf("VerifyRollback() error = %v", err)
	}
	if !rollback.Passed || !rollback.PreviousArtifactsVerified || !rollback.ComisConfigValidated ||
		!rollback.DevCrewServiceOpened || rollback.SQLiteFiles < 3 || !probe.called {
		t.Fatalf("rollback evidence = %#v, probe=%#v", rollback, probe)
	}
}

func TestCreateRecoveryBackupWaitsForRestartedSecretResidencyOracleReadiness(t *testing.T) {
	manifest := recoveryManifestFixture(t)
	executor := &recoveryExecutorFixture{manifest: manifest, unavailableResidencyRuns: 2, malformedResidencyRuns: 1}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	waits := 0
	wait := func(ctx context.Context, _ time.Duration) error {
		waits++
		return ctx.Err()
	}
	backup, err := createRecoveryBackup(
		context.Background(), manifest, filepath.Join(parent, "backup"), executor, manifest.EndedAtMs, wait,
	)
	if err != nil {
		t.Fatalf("createRecoveryBackup() error = %v", err)
	}
	if !backup.Passed || !backup.SecretResidencyPassed {
		t.Fatalf("backup evidence = %#v", backup)
	}
	if executor.residencyRuns != 4 || waits != 3 {
		t.Fatalf("residency runs = %d and readiness waits = %d, want 4 and 3", executor.residencyRuns, waits)
	}
}

func TestCreateRecoveryBackupRefusesPermanentlyUnavailableSecretResidencyOracle(t *testing.T) {
	manifest := recoveryManifestFixture(t)
	executor := &recoveryExecutorFixture{
		manifest: manifest, unavailableResidencyRuns: recoverySecretResidencyReadinessAttempts + 5,
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(parent, "backup")
	wait := func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	_, err := createRecoveryBackup(context.Background(), manifest, backupRoot, executor, manifest.EndedAtMs, wait)
	if err == nil || !strings.Contains(err.Error(), "secret residency oracle is unavailable") {
		t.Fatalf("expected bounded readiness refusal, got %v", err)
	}
	if executor.residencyRuns != recoverySecretResidencyReadinessAttempts {
		t.Fatalf("residency runs = %d, want %d", executor.residencyRuns, recoverySecretResidencyReadinessAttempts)
	}
	if _, err := os.Lstat(backupRoot); !os.IsNotExist(err) {
		t.Fatalf("refused backup root was retained: %v", err)
	}
}

func TestCreateRecoveryBackupEndsReadinessWaitWhenTheContextEnds(t *testing.T) {
	manifest := recoveryManifestFixture(t)
	executor := &recoveryExecutorFixture{manifest: manifest, unavailableResidencyRuns: 4}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wait := func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	_, err := createRecoveryBackup(
		ctx, manifest, filepath.Join(parent, "backup"), executor, manifest.EndedAtMs, wait,
	)
	if err == nil || !strings.Contains(err.Error(), "secret residency oracle is unavailable") {
		t.Fatalf("expected cancelled readiness refusal, got %v", err)
	}
	if executor.residencyRuns != 1 {
		t.Fatalf("residency runs = %d, want 1", executor.residencyRuns)
	}
}

func TestCreateRecoveryBackupRefusesParsedPlaintextSecretResidencyWithoutRetrying(t *testing.T) {
	manifest := recoveryManifestFixture(t)
	executor := &recoveryExecutorFixture{
		manifest: manifest,
		residencyReport: `{"schemaVersion":1,"scannedFiles":8,"readErrors":[],"totalMatches":1,` +
			`"secrets":{"TELEGRAM_BOT_TOKEN":{"retrieved":true,"totalMatches":1},` +
			`"GITHUB_TOKEN":{"retrieved":true,"totalMatches":0}}}`,
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	backupRoot := filepath.Join(parent, "backup")
	waits := 0
	wait := func(ctx context.Context, _ time.Duration) error {
		waits++
		return ctx.Err()
	}
	_, err := createRecoveryBackup(context.Background(), manifest, backupRoot, executor, manifest.EndedAtMs, wait)
	if err == nil || !strings.Contains(err.Error(), "incomplete or found plaintext") {
		t.Fatalf("expected immediate plaintext refusal, got %v", err)
	}
	if executor.residencyRuns != 1 || waits != 0 {
		t.Fatalf("residency runs = %d and readiness waits = %d, want 1 and 0", executor.residencyRuns, waits)
	}
	if _, err := os.Lstat(backupRoot); !os.IsNotExist(err) {
		t.Fatalf("refused backup root was retained: %v", err)
	}
}

func TestRecoveryEvidenceRoundTripRequiresEveryPassingStage(t *testing.T) {
	manifest := validManifest()
	evidence := validRecoveryEvidence(manifest)
	if err := VerifyRecoveryEvidence(manifest, evidence); err != nil {
		t.Fatalf("VerifyRecoveryEvidence() error = %v", err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "recovery-evidence.json")
	if err := WriteRecoveryEvidence(path, evidence); err != nil {
		t.Fatalf("WriteRecoveryEvidence() error = %v", err)
	}
	loaded, err := LoadRecoveryEvidence(path)
	if err != nil {
		t.Fatalf("LoadRecoveryEvidence() error = %v", err)
	}
	if err := VerifyRecoveryEvidence(manifest, loaded); err != nil {
		t.Fatalf("VerifyRecoveryEvidence(loaded) error = %v", err)
	}
	failed := evidence
	failed.Rollback.Passed = false
	if err := VerifyRecoveryEvidence(manifest, failed); err == nil || !strings.Contains(err.Error(), "rollback") {
		t.Fatalf("expected rollback refusal, got %v", err)
	}
}

func TestRecoveryEvidenceRefusesMissingFreshInstallAndUpgradeProof(t *testing.T) {
	manifest := validManifest()
	contents, err := json.Marshal(validRecoveryEvidence(manifest))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(contents, &payload); err != nil {
		t.Fatal(err)
	}
	delete(payload, "installation")
	contents, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var evidence RecoveryEvidence
	if err := json.Unmarshal(contents, &evidence); err != nil {
		t.Fatal(err)
	}
	if err := VerifyRecoveryEvidence(manifest, evidence); err == nil || !strings.Contains(err.Error(), "installation") {
		t.Fatalf("expected installation-evidence refusal, got %v", err)
	}
}

func validRecoveryEvidence(manifest Manifest) RecoveryEvidence {
	return RecoveryEvidence{
		SchemaVersion: 1,
		Installation: InstallationEvidence{
			SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, Passed: true,
			DevCrewChecksumVerified: true, ComisPackageVerified: true,
			FreshArtifactsVerified: 5, PreviousArtifactsVerified: 5, UpgradedArtifactsVerified: 5,
			CurrentComisVersion:    manifest.Artifacts[0].Version,
			PreviousComisVersion:   manifest.Recovery.PreviousArtifacts[0].Version,
			CurrentDevCrewVersion:  manifest.Artifacts[1].Version,
			PreviousDevCrewVersion: manifest.Recovery.PreviousArtifacts[1].Version,
		},
		Backup: BackupEvidence{
			SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, Passed: true,
			Files: 8, Bytes: 4096, SHA256: strings.Repeat("a", 64),
			PlaintextEnvironmentExcluded: true, SecretResidencyPassed: true,
		},
		Restore: RestoreEvidence{
			SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, Passed: true,
			Files: 8, Bytes: 4096, SHA256: strings.Repeat("a", 64), SQLiteFiles: 3,
			ConfigValidated: true, RepositoryRegistryRestored: true,
		},
		Rollback: RollbackEvidence{
			SchemaVersion: 1, CapturedAtMs: manifest.EndedAtMs, Passed: true,
			PreviousArtifactsVerified: true, ComisConfigValidated: true,
			DevCrewServiceOpened: true, SQLiteFiles: 3,
		},
	}
}

func recoveryManifestFixture(t *testing.T) Manifest {
	t.Helper()
	manifest := validManifest()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest.Comis.DataDir = filepath.Join(root, "comis")
	if err := os.MkdirAll(filepath.Join(manifest.Comis.DataDir, "workspace"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.Comis.DataDir, "config.yaml"), []byte("agents: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.Comis.DataDir, ".env"), []byte("TOKEN=plaintext-fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.Comis.DataDir, "workspace", "state.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.Comis.DatabasePath = filepath.Join(manifest.Comis.DataDir, "memory.db")
	createRecoverySQLite(t, manifest.Comis.DatabasePath)
	createRecoverySQLite(t, filepath.Join(manifest.Comis.DataDir, "secrets.db"))
	manifest.DevCrew.DatabasePath = filepath.Join(root, "devcrew.db")
	createRecoverySQLite(t, manifest.DevCrew.DatabasePath)
	manifest.Recovery.CandidateConfigPath = filepath.Join(root, "candidate.json")
	if err := os.WriteFile(manifest.Recovery.CandidateConfigPath, []byte(`{"repository":"comis-repository","readCredentialFile":"/run/secrets/read"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	syntheticRoot, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(syntheticRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest.Recovery.SyntheticComisDataDir = filepath.Join(syntheticRoot, "comis")
	manifest.Recovery.SyntheticDevCrewDatabasePath = filepath.Join(syntheticRoot, "devcrew.db")
	for index := range manifest.Recovery.PreviousArtifacts {
		artifact := &manifest.Recovery.PreviousArtifacts[index]
		artifact.Path = filepath.Join(root, "previous-"+artifact.Kind)
		contents := []byte("previous-" + artifact.Kind + "\n")
		if err := os.WriteFile(artifact.Path, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		artifact.SHA256 = sha256Hex(contents)
	}
	return manifest
}

func createRecoverySQLite(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec("CREATE TABLE recovery_fixture (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO recovery_fixture(value) VALUES ('fixture')"); err != nil {
		t.Fatal(err)
	}
}
