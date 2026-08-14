package livecampaign

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recoveryExecutorFixture struct {
	manifest Manifest
	calls    []Command
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

func recoveryManifestFixture(t *testing.T) Manifest {
	t.Helper()
	manifest := validManifest()
	root := t.TempDir()
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
	manifest.Recovery.SyntheticComisDataDir = filepath.Join(root, "synthetic-comis")
	manifest.Recovery.SyntheticDevCrewDatabasePath = filepath.Join(root, "synthetic-devcrew.db")
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
