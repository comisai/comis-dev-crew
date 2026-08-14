package livecampaign

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumRecoveryEvidenceBytes = 64 * 1024

type RecoveryEvidence struct {
	SchemaVersion int              `json:"schemaVersion"`
	Backup        BackupEvidence   `json:"backup"`
	Restore       RestoreEvidence  `json:"restore"`
	Rollback      RollbackEvidence `json:"rollback"`
}

// RunRecoveryVerification executes backup, isolated restore, and previous-binary rollback.
func RunRecoveryVerification(
	ctx context.Context,
	manifest Manifest,
	backupRoot string,
	restoreRoot string,
	executor Executor,
	probe RollbackServiceProbe,
	capturedAtMs int64,
) (RecoveryEvidence, error) {
	backup, err := CreateRecoveryBackup(ctx, manifest, backupRoot, executor, capturedAtMs)
	if err != nil {
		return RecoveryEvidence{}, err
	}
	restore, err := RestoreRecoveryBackup(ctx, manifest, backupRoot, restoreRoot, executor, capturedAtMs)
	if err != nil {
		return RecoveryEvidence{}, err
	}
	rollback, err := VerifyRollback(ctx, manifest, backupRoot, executor, probe, capturedAtMs)
	if err != nil {
		return RecoveryEvidence{}, err
	}
	evidence := RecoveryEvidence{SchemaVersion: 1, Backup: backup, Restore: restore, Rollback: rollback}
	if err := VerifyRecoveryEvidence(manifest, evidence); err != nil {
		return RecoveryEvidence{}, err
	}
	return evidence, nil
}

// VerifyRecoveryEvidence rejects incomplete, cross-window, or inconsistent recovery claims.
func VerifyRecoveryEvidence(manifest Manifest, evidence RecoveryEvidence) error {
	if err := manifest.validate(); err != nil {
		return fmt.Errorf("verify recovery evidence: %w", err)
	}
	if evidence.SchemaVersion != 1 || evidence.Backup.SchemaVersion != 1 ||
		evidence.Restore.SchemaVersion != 1 || evidence.Rollback.SchemaVersion != 1 {
		return errors.New("verify recovery evidence: every schemaVersion must equal 1")
	}
	for name, capturedAtMs := range map[string]int64{
		"backup": evidence.Backup.CapturedAtMs, "restore": evidence.Restore.CapturedAtMs,
		"rollback": evidence.Rollback.CapturedAtMs,
	} {
		if capturedAtMs < manifest.StartedAtMs || capturedAtMs > manifest.EndedAtMs {
			return fmt.Errorf("verify recovery evidence: %s capture time is outside the campaign", name)
		}
	}
	if !evidence.Backup.Passed || evidence.Backup.Files <= 0 || evidence.Backup.Bytes <= 0 ||
		!lowerHexDigestPattern.MatchString(evidence.Backup.SHA256) ||
		!evidence.Backup.PlaintextEnvironmentExcluded || !evidence.Backup.SecretResidencyPassed {
		return errors.New("verify recovery evidence: backup did not pass every required oracle")
	}
	if !evidence.Restore.Passed || evidence.Restore.Files != evidence.Backup.Files ||
		evidence.Restore.Bytes != evidence.Backup.Bytes || evidence.Restore.SHA256 != evidence.Backup.SHA256 ||
		evidence.Restore.SQLiteFiles < 2 || !evidence.Restore.ConfigValidated ||
		!evidence.Restore.RepositoryRegistryRestored {
		return errors.New("verify recovery evidence: restore did not reproduce and validate the backup")
	}
	if !evidence.Rollback.Passed || !evidence.Rollback.PreviousArtifactsVerified ||
		!evidence.Rollback.ComisConfigValidated || !evidence.Rollback.DevCrewServiceOpened ||
		evidence.Rollback.SQLiteFiles < 2 {
		return errors.New("verify recovery evidence: rollback did not pass previous-binary probes")
	}
	return nil
}

// WriteRecoveryEvidence creates one non-overwriting owner-private JSON artifact.
func WriteRecoveryEvidence(path string, evidence RecoveryEvidence) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("write recovery evidence: path must identify one clean absolute file")
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("write recovery evidence: %w", err)
	}
	contents, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return fmt.Errorf("write recovery evidence: encode JSON: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maximumRecoveryEvidenceBytes {
		return errors.New("write recovery evidence: JSON exceeds the bounded size")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("write recovery evidence: target already exists")
	}
	if err != nil {
		return fmt.Errorf("write recovery evidence: create artifact: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write recovery evidence: persist artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write recovery evidence: close artifact: %w", err)
	}
	return nil
}

// LoadRecoveryEvidence strictly decodes one owner-private regular JSON file.
func LoadRecoveryEvidence(path string) (RecoveryEvidence, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return RecoveryEvidence{}, errors.New("load recovery evidence: path must identify one clean absolute file")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 || info.Size() > maximumRecoveryEvidenceBytes {
		return RecoveryEvidence{}, errors.New("load recovery evidence: artifact must be one bounded owner-private regular file")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return RecoveryEvidence{}, fmt.Errorf("load recovery evidence: read artifact: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var evidence RecoveryEvidence
	if err := decoder.Decode(&evidence); err != nil {
		return RecoveryEvidence{}, fmt.Errorf("load recovery evidence: decode strict JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return RecoveryEvidence{}, errors.New("load recovery evidence: trailing JSON is forbidden")
	}
	return evidence, nil
}
