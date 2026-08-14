package livecampaign

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// RollbackServiceProbe proves a previous DevCrew service can open copied state.
type RollbackServiceProbe func(context.Context, string, string, string, string) error

type RollbackEvidence struct {
	SchemaVersion             int   `json:"schemaVersion"`
	CapturedAtMs              int64 `json:"capturedAtMs"`
	Passed                    bool  `json:"passed"`
	PreviousArtifactsVerified bool  `json:"previousArtifactsVerified"`
	ComisConfigValidated      bool  `json:"comisConfigValidated"`
	DevCrewServiceOpened      bool  `json:"devcrewServiceOpened"`
	SQLiteFiles               int   `json:"sqliteFiles"`
}

// VerifyRollback runs previous pinned binaries only against copied synthetic state.
func VerifyRollback(
	ctx context.Context,
	manifest Manifest,
	backupRoot string,
	executor Executor,
	probe RollbackServiceProbe,
	capturedAtMs int64,
) (RollbackEvidence, error) {
	if ctx == nil || executor == nil || probe == nil || capturedAtMs <= 0 {
		return RollbackEvidence{}, errors.New("verify rollback: dependencies and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return RollbackEvidence{}, fmt.Errorf("verify rollback: %w", err)
	}
	if _, err := loadRecoveryManifest(backupRoot); err != nil {
		return RollbackEvidence{}, fmt.Errorf("verify rollback: %w", err)
	}
	if err := validateSyntheticRollbackTargets(manifest); err != nil {
		return RollbackEvidence{}, err
	}
	if err := verifyPreviousArtifacts(ctx, manifest, executor); err != nil {
		return RollbackEvidence{}, err
	}
	if err := materializeSyntheticRollback(manifest, backupRoot); err != nil {
		removeSyntheticRollback(manifest)
		return RollbackEvidence{}, err
	}
	sqliteFiles, err := verifySyntheticRollbackSQLite(manifest)
	if err != nil {
		removeSyntheticRollback(manifest)
		return RollbackEvidence{}, err
	}
	previous := artifactPinsByKind(manifest.Recovery.PreviousArtifacts)
	configPath := filepath.Join(manifest.Recovery.SyntheticComisDataDir, "config.yaml")
	if _, err := executor.Run(ctx, Command{
		Path: manifest.Comis.NodePath,
		Args: []string{previous["comis-cli"].Path, "config", "validate", "-c", configPath},
		Env: map[string]string{
			"COMIS_DATA_DIR": manifest.Recovery.SyntheticComisDataDir, "COMIS_CONFIG_PATHS": configPath,
		},
	}); err != nil {
		removeSyntheticRollback(manifest)
		return RollbackEvidence{}, errors.New("verify rollback: previous Comis CLI rejected the copied configuration")
	}
	socketPath := filepath.Join(filepath.Dir(manifest.Recovery.SyntheticDevCrewDatabasePath), "rollback-devcrew.sock")
	if err := probe(
		ctx, previous["devcrew-service"].Path, previous["devcrew"].Path,
		manifest.Recovery.SyntheticDevCrewDatabasePath, socketPath,
	); err != nil {
		removeSyntheticRollback(manifest)
		return RollbackEvidence{}, fmt.Errorf("verify rollback: previous DevCrew service rejected copied state: %w", err)
	}
	return RollbackEvidence{
		SchemaVersion: 1, CapturedAtMs: capturedAtMs, Passed: true,
		PreviousArtifactsVerified: true, ComisConfigValidated: true,
		DevCrewServiceOpened: true, SQLiteFiles: sqliteFiles,
	}, nil
}

func verifyPreviousArtifacts(ctx context.Context, manifest Manifest, executor Executor) error {
	for _, artifact := range manifest.Recovery.PreviousArtifacts {
		if err := validatePinnedArtifact(artifact); err != nil {
			return fmt.Errorf("verify rollback: previous %s artifact: %w", artifact.Kind, err)
		}
		if err := validatePinnedArtifactVersion(ctx, manifest, artifact, executor); err != nil {
			return fmt.Errorf("verify rollback: previous %s artifact: %w", artifact.Kind, err)
		}
	}
	return nil
}

func validateSyntheticRollbackTargets(manifest Manifest) error {
	for _, path := range []string{
		manifest.Recovery.SyntheticComisDataDir, manifest.Recovery.SyntheticDevCrewDatabasePath,
	} {
		if _, err := os.Lstat(path); err == nil {
			return errors.New("verify rollback: synthetic rollback target already exists")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("verify rollback: inspect synthetic target: %w", err)
		}
		if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
			return fmt.Errorf("verify rollback: synthetic target parent: %w", err)
		}
	}
	return nil
}

func materializeSyntheticRollback(manifest Manifest, backupRoot string) error {
	if err := copyRecoveryTree(
		filepath.Join(backupRoot, "comis-data"), manifest.Recovery.SyntheticComisDataDir, false,
	); err != nil {
		return fmt.Errorf("verify rollback: copy synthetic Comis state: %w", err)
	}
	if err := copyRecoveryFile(
		filepath.Join(backupRoot, "comis-data", ".devcrew-recovery", "devcrew.db"),
		manifest.Recovery.SyntheticDevCrewDatabasePath,
	); err != nil {
		return fmt.Errorf("verify rollback: copy synthetic DevCrew state: %w", err)
	}
	return nil
}

func verifySyntheticRollbackSQLite(manifest Manifest) (int, error) {
	paths := []string{
		filepath.Join(manifest.Recovery.SyntheticComisDataDir, "memory.db"),
		filepath.Join(manifest.Recovery.SyntheticComisDataDir, "secrets.db"),
		manifest.Recovery.SyntheticDevCrewDatabasePath,
	}
	verified := 0
	for _, path := range paths {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) && filepath.Base(path) == "secrets.db" {
			continue
		}
		if statErr != nil || !info.Mode().IsRegular() {
			return 0, errors.New("verify rollback: required synthetic SQLite file is unavailable")
		}
		if err := sqliteIntegrity(path); err != nil {
			return 0, fmt.Errorf("verify rollback: synthetic SQLite integrity failed: %w", err)
		}
		verified++
	}
	return verified, nil
}

func removeSyntheticRollback(manifest Manifest) {
	_ = os.RemoveAll(manifest.Recovery.SyntheticComisDataDir)
	_ = os.Remove(manifest.Recovery.SyntheticDevCrewDatabasePath)
	_ = os.Remove(filepath.Join(filepath.Dir(manifest.Recovery.SyntheticDevCrewDatabasePath), "rollback-devcrew.sock"))
}

func artifactPinsByKind(artifacts []ArtifactPin) map[string]ArtifactPin {
	result := make(map[string]ArtifactPin, len(artifacts))
	for _, artifact := range artifacts {
		result[artifact.Kind] = artifact
	}
	return result
}
