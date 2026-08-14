package livecampaign

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumRecoveryManifestBytes = 4 * 1024 * 1024

type BackupEvidence struct {
	SchemaVersion                int    `json:"schemaVersion"`
	CapturedAtMs                 int64  `json:"capturedAtMs"`
	Passed                       bool   `json:"passed"`
	Files                        int    `json:"files"`
	Bytes                        int64  `json:"bytes"`
	SHA256                       string `json:"sha256"`
	PlaintextEnvironmentExcluded bool   `json:"plaintextEnvironmentExcluded"`
	SecretResidencyPassed        bool   `json:"secretResidencyPassed"`
}

type RestoreEvidence struct {
	SchemaVersion              int    `json:"schemaVersion"`
	CapturedAtMs               int64  `json:"capturedAtMs"`
	Passed                     bool   `json:"passed"`
	Files                      int    `json:"files"`
	Bytes                      int64  `json:"bytes"`
	SHA256                     string `json:"sha256"`
	SQLiteFiles                int    `json:"sqliteFiles"`
	ConfigValidated            bool   `json:"configValidated"`
	RepositoryRegistryRestored bool   `json:"repositoryRegistryRestored"`
}

type recoveryManifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	CampaignID    string         `json:"campaignId"`
	Source        SourcePins     `json:"source"`
	Files         []recoveryFile `json:"files"`
	SHA256        string         `json:"sha256"`
}

// CreateRecoveryBackup stops only the isolated units and creates one private backup tree.
func CreateRecoveryBackup(
	ctx context.Context,
	manifest Manifest,
	backupRoot string,
	executor Executor,
	capturedAtMs int64,
) (BackupEvidence, error) {
	if ctx == nil || executor == nil || capturedAtMs <= 0 {
		return BackupEvidence{}, errors.New("create recovery backup: dependencies and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return BackupEvidence{}, fmt.Errorf("create recovery backup: %w", err)
	}
	if err := validateRecoveryOutputRoot(manifest, backupRoot); err != nil {
		return BackupEvidence{}, err
	}
	if err := ensurePrivateDirectory(filepath.Dir(backupRoot)); err != nil {
		return BackupEvidence{}, err
	}
	if err := os.Mkdir(backupRoot, 0o700); err != nil {
		return BackupEvidence{}, fmt.Errorf("create recovery backup: reserve output: %w", err)
	}
	stopped, err := stopRecoveryServices(ctx, manifest, executor)
	if err != nil {
		_ = restartRecoveryServices(manifest, executor, stopped)
		_ = os.RemoveAll(backupRoot)
		return BackupEvidence{}, err
	}
	buildErr := buildRecoveryBackup(ctx, manifest, backupRoot, executor)
	startErr := restartRecoveryServices(manifest, executor, stopped)
	if buildErr != nil || startErr != nil {
		_ = os.RemoveAll(backupRoot)
		if buildErr != nil {
			return BackupEvidence{}, buildErr
		}
		return BackupEvidence{}, startErr
	}
	archive, err := loadRecoveryManifest(backupRoot)
	if err != nil {
		_ = os.RemoveAll(backupRoot)
		return BackupEvidence{}, err
	}
	if err := verifyBackupSecretResidency(ctx, manifest, backupRoot, executor); err != nil {
		_ = os.RemoveAll(backupRoot)
		return BackupEvidence{}, err
	}
	_, bytes, _ := recoveryDigest(archive.Files)
	return BackupEvidence{
		SchemaVersion: 1, CapturedAtMs: capturedAtMs, Passed: true,
		Files: len(archive.Files), Bytes: bytes, SHA256: archive.SHA256,
		PlaintextEnvironmentExcluded: true, SecretResidencyPassed: true,
	}, nil
}

func buildRecoveryBackup(ctx context.Context, manifest Manifest, backupRoot string, executor Executor) error {
	comisRoot := filepath.Join(backupRoot, "comis-data")
	if err := copyRecoveryTree(manifest.Comis.DataDir, comisRoot, true); err != nil {
		return fmt.Errorf("create recovery backup: copy Comis data: %w", err)
	}
	recoveryRoot := filepath.Join(comisRoot, ".devcrew-recovery")
	if err := os.Mkdir(recoveryRoot, 0o700); err != nil {
		return fmt.Errorf("create recovery backup: create DevCrew payload: %w", err)
	}
	if err := copyRecoveryFile(manifest.DevCrew.DatabasePath, filepath.Join(recoveryRoot, "devcrew.db")); err != nil {
		return fmt.Errorf("create recovery backup: copy DevCrew database: %w", err)
	}
	if err := copyRecoveryFile(manifest.Recovery.CandidateConfigPath, filepath.Join(recoveryRoot, "candidate.json")); err != nil {
		return fmt.Errorf("create recovery backup: copy candidate configuration: %w", err)
	}
	unitRoot := filepath.Join(recoveryRoot, "service-units")
	if err := os.Mkdir(unitRoot, 0o700); err != nil {
		return fmt.Errorf("create recovery backup: create service-unit payload: %w", err)
	}
	for _, unit := range expectedResourceUnits(manifest) {
		definition, err := executor.Run(ctx, Command{Path: manifest.Services.SystemctlPath, Args: []string{"cat", unit}})
		if err != nil || len(definition) == 0 || len(definition) > maximumCommandOutputBytes {
			return fmt.Errorf("create recovery backup: capture service unit %s", unit)
		}
		if err := os.WriteFile(filepath.Join(unitRoot, unit+".txt"), definition, 0o600); err != nil {
			return fmt.Errorf("create recovery backup: write service unit %s: %w", unit, err)
		}
	}
	files, err := recoveryFiles(backupRoot)
	if err != nil {
		return fmt.Errorf("create recovery backup: inventory payload: %w", err)
	}
	digest, _, err := recoveryDigest(files)
	if err != nil {
		return fmt.Errorf("create recovery backup: digest payload: %w", err)
	}
	archive := recoveryManifest{
		SchemaVersion: 1, CampaignID: manifest.CampaignID, Source: manifest.Source,
		Files: files, SHA256: digest,
	}
	contents, err := json.MarshalIndent(archive, "", "  ")
	if err != nil {
		return fmt.Errorf("create recovery backup: encode manifest: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maximumRecoveryManifestBytes {
		return errors.New("create recovery backup: manifest exceeds the bounded size")
	}
	if err := os.WriteFile(filepath.Join(backupRoot, "recovery-manifest.json"), contents, 0o600); err != nil {
		return fmt.Errorf("create recovery backup: write manifest: %w", err)
	}
	return nil
}

// RestoreRecoveryBackup copies and verifies one backup into an isolated restore root.
func RestoreRecoveryBackup(
	ctx context.Context,
	manifest Manifest,
	backupRoot string,
	restoreRoot string,
	executor Executor,
	capturedAtMs int64,
) (RestoreEvidence, error) {
	if ctx == nil || executor == nil || capturedAtMs <= 0 {
		return RestoreEvidence{}, errors.New("restore recovery backup: dependencies and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return RestoreEvidence{}, fmt.Errorf("restore recovery backup: %w", err)
	}
	if err := validateRecoveryOutputRoot(manifest, restoreRoot); err != nil {
		return RestoreEvidence{}, err
	}
	archive, err := loadRecoveryManifest(backupRoot)
	if err != nil {
		return RestoreEvidence{}, err
	}
	if archive.CampaignID != manifest.CampaignID || archive.Source != manifest.Source {
		return RestoreEvidence{}, errors.New("restore recovery backup: campaign or source identity differs")
	}
	if err := ensurePrivateDirectory(filepath.Dir(restoreRoot)); err != nil {
		return RestoreEvidence{}, err
	}
	if err := copyRecoveryTree(backupRoot, restoreRoot, false); err != nil {
		_ = os.RemoveAll(restoreRoot)
		return RestoreEvidence{}, fmt.Errorf("restore recovery backup: copy artifact: %w", err)
	}
	restoredFiles, err := recoveryFiles(restoreRoot)
	if err != nil || !equalRecoveryFiles(restoredFiles, archive.Files) {
		_ = os.RemoveAll(restoreRoot)
		return RestoreEvidence{}, errors.New("restore recovery backup: restored file inventory differs")
	}
	digest, bytes, err := recoveryDigest(restoredFiles)
	if err != nil || digest != archive.SHA256 {
		_ = os.RemoveAll(restoreRoot)
		return RestoreEvidence{}, errors.New("restore recovery backup: restored digest differs")
	}
	sqliteFiles, err := verifyRestoredSQLite(restoreRoot)
	if err != nil {
		_ = os.RemoveAll(restoreRoot)
		return RestoreEvidence{}, err
	}
	configPath := filepath.Join(restoreRoot, "comis-data", "config.yaml")
	if _, err := executor.Run(ctx, Command{
		Path: manifest.Comis.NodePath,
		Args: []string{manifest.Comis.CLIScriptPath, "config", "validate", "-c", configPath},
		Env:  map[string]string{"COMIS_DATA_DIR": filepath.Join(restoreRoot, "comis-data"), "COMIS_CONFIG_PATHS": configPath},
	}); err != nil {
		_ = os.RemoveAll(restoreRoot)
		return RestoreEvidence{}, errors.New("restore recovery backup: restored Comis configuration is invalid")
	}
	registryRestored := restoredRegistryPresent(manifest, restoreRoot)
	if !registryRestored {
		_ = os.RemoveAll(restoreRoot)
		return RestoreEvidence{}, errors.New("restore recovery backup: repository registry unit definitions are incomplete")
	}
	return RestoreEvidence{
		SchemaVersion: 1, CapturedAtMs: capturedAtMs, Passed: true,
		Files: len(restoredFiles), Bytes: bytes, SHA256: digest, SQLiteFiles: sqliteFiles,
		ConfigValidated: true, RepositoryRegistryRestored: true,
	}, nil
}

func loadRecoveryManifest(root string) (recoveryManifest, error) {
	path := filepath.Join(root, "recovery-manifest.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 || info.Size() > maximumRecoveryManifestBytes {
		return recoveryManifest{}, errors.New("recovery manifest must be one bounded owner-private regular file")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return recoveryManifest{}, fmt.Errorf("read recovery manifest: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var archive recoveryManifest
	if err := decoder.Decode(&archive); err != nil {
		return recoveryManifest{}, fmt.Errorf("decode strict recovery manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return recoveryManifest{}, errors.New("decode strict recovery manifest: trailing JSON is forbidden")
	}
	files, err := recoveryFiles(root)
	if err != nil || !equalRecoveryFiles(files, archive.Files) {
		return recoveryManifest{}, errors.New("recovery artifact file inventory differs from its manifest")
	}
	digest, _, err := recoveryDigest(files)
	if archive.SchemaVersion != 1 || err != nil || digest != archive.SHA256 {
		return recoveryManifest{}, errors.New("recovery artifact digest or schema is invalid")
	}
	return archive, nil
}

func verifyRestoredSQLite(restoreRoot string) (int, error) {
	paths := []string{
		filepath.Join(restoreRoot, "comis-data", "memory.db"),
		filepath.Join(restoreRoot, "comis-data", "secrets.db"),
		filepath.Join(restoreRoot, "comis-data", ".devcrew-recovery", "devcrew.db"),
	}
	verified := 0
	for _, path := range paths {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) && filepath.Base(path) == "secrets.db" {
			continue
		}
		if statErr != nil || !info.Mode().IsRegular() {
			return 0, errors.New("restore recovery backup: required SQLite file is unavailable")
		}
		if err := sqliteIntegrity(path); err != nil {
			return 0, fmt.Errorf("restore recovery backup: SQLite integrity failed: %w", err)
		}
		verified++
	}
	return verified, nil
}

func sqliteIntegrity(path string) error {
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer database.Close()
	var result string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return errors.New("SQLite integrity check did not return ok")
	}
	return nil
}

func restoredRegistryPresent(manifest Manifest, restoreRoot string) bool {
	recoveryRoot := filepath.Join(restoreRoot, "comis-data", ".devcrew-recovery")
	if info, err := os.Lstat(filepath.Join(recoveryRoot, "candidate.json")); err != nil || !info.Mode().IsRegular() {
		return false
	}
	root := filepath.Join(recoveryRoot, "service-units")
	for _, unit := range expectedResourceUnits(manifest) {
		if info, err := os.Lstat(filepath.Join(root, unit+".txt")); err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}
