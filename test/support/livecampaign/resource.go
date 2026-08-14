package livecampaign

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	minimumResourceObservationMs      = int64(60 * 60 * 1000)
	maximumServiceMemoryGrowthBytes   = int64(512 * 1024 * 1024)
	maximumServiceOpenFDGrowth        = 256
	maximumComisDataGrowthBytes       = int64(1024 * 1024 * 1024)
	maximumDevCrewDatabaseGrowthBytes = int64(128 * 1024 * 1024)
	maximumDevCrewDatabaseBytes       = int64(256 * 1024 * 1024)
)

// ServiceResourceSnapshot records bounded, content-free systemd process metrics.
type ServiceResourceSnapshot struct {
	Unit                string `json:"unit"`
	MainPID             uint64 `json:"mainPid"`
	MemoryBytes         int64  `json:"memoryBytes"`
	Tasks               int    `json:"tasks"`
	OpenFileDescriptors int    `json:"openFileDescriptors"`
}

// DataResourceSnapshot records regular-file counts and total bytes under one root.
type DataResourceSnapshot struct {
	RegularFiles int   `json:"regularFiles"`
	Bytes        int64 `json:"bytes"`
}

// ResourceSnapshot is one resource observation tied to the campaign clock.
type ResourceSnapshot struct {
	CapturedAtMs         int64                     `json:"capturedAtMs"`
	Services             []ServiceResourceSnapshot `json:"services"`
	ComisData            DataResourceSnapshot      `json:"comisData"`
	DevCrewDatabaseBytes int64                     `json:"devcrewDatabaseBytes"`
	WorktreeDirectories  int                       `json:"worktreeDirectories"`
}

// ResourceObservation binds the start and finish samples for one campaign.
type ResourceObservation struct {
	SchemaVersion int              `json:"schemaVersion"`
	Started       ResourceSnapshot `json:"started"`
	Finished      ResourceSnapshot `json:"finished"`
}

type resourceReader interface {
	RegularFileBytes(string) (int64, error)
	TreeTotals(string) (int, int64, error)
	DirectoryCount(string) (int, error)
	OpenFDCount(uint64) (int, error)
}

type realResourceReader struct{}

// CaptureResourceSnapshot reads live resource state from the protected runner.
func CaptureResourceSnapshot(
	ctx context.Context,
	manifest Manifest,
	executor Executor,
	capturedAtMs int64,
) (ResourceSnapshot, error) {
	return captureResourceSnapshot(ctx, manifest, executor, realResourceReader{}, capturedAtMs)
}

func captureResourceSnapshot(
	ctx context.Context,
	manifest Manifest,
	executor Executor,
	reader resourceReader,
	capturedAtMs int64,
) (ResourceSnapshot, error) {
	if ctx == nil || executor == nil || reader == nil || capturedAtMs <= 0 {
		return ResourceSnapshot{}, errors.New("capture resources: context, executor, reader, and capture time are required")
	}
	if err := manifest.validate(); err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: %w", err)
	}
	snapshot := ResourceSnapshot{CapturedAtMs: capturedAtMs}
	for _, unit := range expectedResourceUnits(manifest) {
		service, err := captureServiceResources(ctx, manifest.Services.SystemctlPath, unit, executor, reader)
		if err != nil {
			return ResourceSnapshot{}, err
		}
		snapshot.Services = append(snapshot.Services, service)
	}
	files, bytes, err := reader.TreeTotals(manifest.Comis.DataDir)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect Comis data root: %w", err)
	}
	snapshot.ComisData = DataResourceSnapshot{RegularFiles: files, Bytes: bytes}
	snapshot.DevCrewDatabaseBytes, err = reader.RegularFileBytes(manifest.DevCrew.DatabasePath)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect DevCrew database: %w", err)
	}
	snapshot.WorktreeDirectories, err = reader.DirectoryCount(manifest.DevCrew.WorktreeRoot)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect DevCrew worktree root: %w", err)
	}
	return snapshot, nil
}

func captureServiceResources(
	ctx context.Context,
	systemctlPath string,
	unit string,
	executor Executor,
	reader resourceReader,
) (ServiceResourceSnapshot, error) {
	values := make(map[string]uint64, 3)
	for _, property := range []string{"MainPID", "MemoryCurrent", "TasksCurrent"} {
		output, err := executor.Run(ctx, Command{
			Path: systemctlPath, Args: []string{"show", unit, "--property", property, "--value"},
		})
		if err != nil {
			return ServiceResourceSnapshot{}, fmt.Errorf("capture resources: read %s for service %s", property, unit)
		}
		value, err := strconv.ParseUint(strings.TrimSpace(string(output)), 10, 64)
		if err != nil || value == 0 {
			return ServiceResourceSnapshot{}, fmt.Errorf("capture resources: service %s property %s is invalid", unit, property)
		}
		values[property] = value
	}
	if values["MemoryCurrent"] > uint64(^uint64(0)>>1) || values["TasksCurrent"] > uint64(^uint(0)>>1) {
		return ServiceResourceSnapshot{}, fmt.Errorf("capture resources: service %s resource value exceeds local bounds", unit)
	}
	openFDs, err := reader.OpenFDCount(values["MainPID"])
	if err != nil || openFDs <= 0 {
		return ServiceResourceSnapshot{}, fmt.Errorf("capture resources: inspect open file descriptors for service %s", unit)
	}
	return ServiceResourceSnapshot{
		Unit: unit, MainPID: values["MainPID"], MemoryBytes: int64(values["MemoryCurrent"]),
		Tasks: int(values["TasksCurrent"]), OpenFileDescriptors: openFDs,
	}, nil
}

// VerifyResourceObservation rejects short, incomplete, or leaking observations.
func VerifyResourceObservation(manifest Manifest, observation ResourceObservation) error {
	if err := manifest.validate(); err != nil {
		return fmt.Errorf("verify resource observation: %w", err)
	}
	if observation.SchemaVersion != 1 {
		return errors.New("verify resource observation: schemaVersion must equal 1")
	}
	if observation.Started.CapturedAtMs < manifest.StartedAtMs ||
		observation.Finished.CapturedAtMs > manifest.EndedAtMs ||
		observation.Finished.CapturedAtMs-observation.Started.CapturedAtMs < minimumResourceObservationMs {
		return errors.New("verify resource observation: samples must remain inside the campaign and span at least one hour")
	}
	started, err := indexedResourceServices(manifest, observation.Started)
	if err != nil {
		return err
	}
	finished, err := indexedResourceServices(manifest, observation.Finished)
	if err != nil {
		return err
	}
	for _, unit := range expectedResourceUnits(manifest) {
		if growth := finished[unit].MemoryBytes - started[unit].MemoryBytes; growth > maximumServiceMemoryGrowthBytes {
			return fmt.Errorf("verify resource observation: service %s memory growth exceeds the bounded allowance", unit)
		}
		if growth := finished[unit].OpenFileDescriptors - started[unit].OpenFileDescriptors; growth > maximumServiceOpenFDGrowth {
			return fmt.Errorf("verify resource observation: service %s file descriptor growth exceeds the bounded allowance", unit)
		}
	}
	if observation.Started.WorktreeDirectories != len(manifest.Tasks) || observation.Finished.WorktreeDirectories != 0 {
		return errors.New("verify resource observation: worktree counts do not prove two active lanes followed by complete cleanup")
	}
	if err := verifyDataResourceBounds(observation); err != nil {
		return err
	}
	return nil
}

func indexedResourceServices(manifest Manifest, snapshot ResourceSnapshot) (map[string]ServiceResourceSnapshot, error) {
	expected := expectedResourceUnits(manifest)
	if len(snapshot.Services) != len(expected) {
		return nil, errors.New("verify resource observation: service samples are incomplete")
	}
	indexed := make(map[string]ServiceResourceSnapshot, len(expected))
	for _, service := range snapshot.Services {
		if !contains(expected, service.Unit) || service.MainPID == 0 || service.MemoryBytes <= 0 ||
			service.Tasks <= 0 || service.OpenFileDescriptors <= 0 {
			return nil, errors.New("verify resource observation: service sample is invalid")
		}
		if _, exists := indexed[service.Unit]; exists {
			return nil, errors.New("verify resource observation: service samples are duplicated")
		}
		indexed[service.Unit] = service
	}
	return indexed, nil
}

func verifyDataResourceBounds(observation ResourceObservation) error {
	if observation.Started.ComisData.RegularFiles <= 0 || observation.Started.ComisData.Bytes <= 0 ||
		observation.Finished.ComisData.RegularFiles <= 0 || observation.Finished.ComisData.Bytes <= 0 {
		return errors.New("verify resource observation: Comis data metrics are incomplete")
	}
	if observation.Finished.ComisData.Bytes-observation.Started.ComisData.Bytes > maximumComisDataGrowthBytes {
		return errors.New("verify resource observation: Comis data growth exceeds the bounded allowance")
	}
	if observation.Started.DevCrewDatabaseBytes <= 0 || observation.Finished.DevCrewDatabaseBytes <= 0 ||
		observation.Finished.DevCrewDatabaseBytes > maximumDevCrewDatabaseBytes ||
		observation.Finished.DevCrewDatabaseBytes-observation.Started.DevCrewDatabaseBytes > maximumDevCrewDatabaseGrowthBytes {
		return errors.New("verify resource observation: DevCrew database size or growth exceeds the bounded allowance")
	}
	return nil
}

func expectedResourceUnits(manifest Manifest) []string {
	return []string{manifest.Services.MCPUnit, manifest.Services.DevCrewUnit, manifest.Services.ComisUnit}
}

func (realResourceReader) RegularFileBytes(path string) (int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if !info.Mode().IsRegular() {
		return 0, errors.New("path is not one regular file")
	}
	return info.Size(), nil
}

func (realResourceReader) TreeTotals(root string) (int, int64, error) {
	files := 0
	var bytes int64
	err := filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("resource tree contains an unexpected symbolic link")
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files++
		bytes += info.Size()
		return nil
	})
	return files, bytes, err
}

func (realResourceReader) DirectoryCount(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			return 0, errors.New("worktree root contains an unexpected non-directory entry")
		}
		count++
	}
	return count, nil
}

func (realResourceReader) OpenFDCount(pid uint64) (int, error) {
	entries, err := os.ReadDir(filepath.Join("/proc", strconv.FormatUint(pid, 10), "fd"))
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}
