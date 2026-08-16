package livecampaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func validateRecoveryOutputRoot(manifest Manifest, path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("recovery output must be one clean absolute path")
	}
	for _, protected := range []string{
		manifest.Comis.DataDir, filepath.Dir(manifest.DevCrew.DatabasePath), manifest.DevCrew.CodeRoot,
		manifest.Comis.CodeRoot, manifest.GitHub.PrimaryCheckout, manifest.DevCrew.WorktreeRoot,
	} {
		if pathWithin(protected, path) || pathWithin(path, protected) {
			return errors.New("recovery output must not overlap live campaign state or code")
		}
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("recovery output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect recovery output: %w", err)
	}
	return nil
}

func stopRecoveryServices(ctx context.Context, manifest Manifest, executor Executor) ([]string, error) {
	units := expectedResourceUnits(manifest)
	stopped := make([]string, 0, len(units))
	for _, unit := range units {
		if _, err := executor.Run(ctx, Command{
			Path: manifest.Services.SystemctlPath, Args: []string{"stop", unit},
		}); err != nil {
			return stopped, fmt.Errorf("stop isolated recovery unit %s: %w", unit, err)
		}
		stopped = append(stopped, unit)
	}
	return stopped, nil
}

func startRecoveryServices(ctx context.Context, manifest Manifest, executor Executor, stopped []string) error {
	var firstError error
	for index := len(stopped) - 1; index >= 0; index-- {
		unit := stopped[index]
		if _, err := executor.Run(ctx, Command{
			Path: manifest.Services.SystemctlPath, Args: []string{"start", unit},
		}); err != nil && firstError == nil {
			firstError = fmt.Errorf("start isolated recovery unit %s: %w", unit, err)
		}
	}
	return firstError
}

func restartRecoveryServices(manifest Manifest, executor Executor, stopped []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return startRecoveryServices(ctx, manifest, executor, stopped)
}

func verifyBackupSecretResidency(
	ctx context.Context,
	manifest Manifest,
	backupRoot string,
	executor Executor,
) error {
	args := append([]string{manifest.Comis.SecretResidencyScript}, manifest.Comis.SecretNames...)
	var report residencyReport
	output, err := executor.Run(ctx, Command{
		Path: manifest.Comis.NodePath, Args: args,
		UseComisGatewayToken: true,
		Env: map[string]string{
			"RIG_MODE": "local", "COMIS_SRC": manifest.Comis.CodeRoot,
			"COMIS_DATA_DIR":     filepath.Join(backupRoot, "comis-data"),
			"COMIS_CONFIG_PATHS": filepath.Join(backupRoot, "comis-data", "config.yaml"),
		},
	})
	if err != nil || len(output) == 0 || len(output) > maximumCommandOutputBytes || json.Unmarshal(output, &report) != nil {
		return errors.New("create recovery backup: count-only secret residency oracle is unavailable")
	}
	if report.SchemaVersion != 1 || report.ScannedFiles <= 0 || len(report.ReadErrors) != 0 ||
		report.TotalMatches != 0 || len(report.Secrets) != len(manifest.Comis.SecretNames) {
		return errors.New("create recovery backup: count-only secret residency oracle is incomplete or found plaintext")
	}
	for _, name := range manifest.Comis.SecretNames {
		secret, exists := report.Secrets[name]
		if !exists || !secret.Retrieved || secret.TotalMatches != 0 {
			return fmt.Errorf("create recovery backup: secret residency result for %s is incomplete", name)
		}
	}
	return nil
}
