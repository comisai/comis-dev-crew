package livecampaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maximumResourceBaselineBytes = 64 * 1024

// ResourceBaseline is the owner-private start sample for evidence-only closeout.
type ResourceBaseline struct {
	SchemaVersion int              `json:"schemaVersion"`
	CampaignID    string           `json:"campaignId"`
	ComisCommit   string           `json:"comisCommit"`
	DevCrewCommit string           `json:"devcrewCommit"`
	Snapshot      ResourceSnapshot `json:"snapshot"`
}

// NewResourceBaseline binds a start sample to the exact campaign identity.
func NewResourceBaseline(manifest Manifest, snapshot ResourceSnapshot) (ResourceBaseline, error) {
	baseline := ResourceBaseline{
		SchemaVersion: 1, CampaignID: manifest.CampaignID,
		ComisCommit: manifest.Source.ComisCommit, DevCrewCommit: manifest.Source.DevCrewCommit,
		Snapshot: snapshot,
	}
	baseline.Snapshot.Services = append([]ServiceResourceSnapshot(nil), snapshot.Services...)
	if err := VerifyResourceBaseline(manifest, baseline); err != nil {
		return ResourceBaseline{}, err
	}
	return baseline, nil
}

// VerifyResourceBaseline rejects cross-campaign, incomplete, or non-start samples.
func VerifyResourceBaseline(manifest Manifest, baseline ResourceBaseline) error {
	if err := manifest.validate(); err != nil {
		return fmt.Errorf("verify resource baseline: %w", err)
	}
	if baseline.SchemaVersion != 1 || baseline.CampaignID != manifest.CampaignID ||
		baseline.ComisCommit != manifest.Source.ComisCommit || baseline.DevCrewCommit != manifest.Source.DevCrewCommit {
		return errors.New("verify resource baseline: campaign or source identity differs from the manifest")
	}
	if baseline.Snapshot.CapturedAtMs < manifest.StartedAtMs || baseline.Snapshot.CapturedAtMs > manifest.EndedAtMs {
		return errors.New("verify resource baseline: capture time is outside the campaign")
	}
	if _, err := indexedResourceServices(manifest, baseline.Snapshot); err != nil {
		return fmt.Errorf("verify resource baseline: %w", err)
	}
	if baseline.Snapshot.WorktreeDirectories != len(manifest.Tasks) {
		return errors.New("verify resource baseline: starting worktree count does not prove both task lanes")
	}
	if baseline.Snapshot.ComisData.RegularFiles <= 0 || baseline.Snapshot.ComisData.Bytes <= 0 ||
		baseline.Snapshot.DevCrewDatabaseBytes <= 0 ||
		baseline.Snapshot.DevCrewDatabaseBytes > maximumDevCrewDatabaseBytes {
		return errors.New("verify resource baseline: data metrics are incomplete or exceed the bounded allowance")
	}
	return nil
}

// WriteResourceBaseline creates one non-overwriting owner-private JSON artifact.
func WriteResourceBaseline(path string, baseline ResourceBaseline) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." {
		return errors.New("write resource baseline: path must identify one clean absolute file")
	}
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("write resource baseline: %w", err)
	}
	contents, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("write resource baseline: encode JSON: %w", err)
	}
	contents = append(contents, '\n')
	if len(contents) > maximumResourceBaselineBytes {
		return errors.New("write resource baseline: JSON exceeds the bounded size")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return errors.New("write resource baseline: target already exists")
	}
	if err != nil {
		return fmt.Errorf("write resource baseline: create artifact: %w", err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write resource baseline: persist artifact: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("write resource baseline: close artifact: %w", err)
	}
	return nil
}

// LoadResourceBaseline strictly decodes one owner-private regular JSON file.
func LoadResourceBaseline(path string) (ResourceBaseline, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ResourceBaseline{}, errors.New("load resource baseline: path must identify one clean absolute file")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return ResourceBaseline{}, fmt.Errorf("load resource baseline: inspect artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return ResourceBaseline{}, errors.New("load resource baseline: artifact must be one owner-private regular file")
	}
	if info.Size() <= 0 || info.Size() > maximumResourceBaselineBytes {
		return ResourceBaseline{}, errors.New("load resource baseline: artifact size is outside the bounded range")
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return ResourceBaseline{}, fmt.Errorf("load resource baseline: read artifact: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var baseline ResourceBaseline
	if err := decoder.Decode(&baseline); err != nil {
		return ResourceBaseline{}, fmt.Errorf("load resource baseline: decode strict JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ResourceBaseline{}, errors.New("load resource baseline: trailing JSON is forbidden")
	}
	return baseline, nil
}
