package livecampaign

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	minimumResourceObservationMs      = int64(60 * 60 * 1000)
	maximumServiceMemoryGrowthBytes   = int64(512 * 1024 * 1024)
	maximumServiceRSSGrowthBytes      = int64(256 * 1024 * 1024)
	maximumServiceOpenFDGrowth        = 256
	maximumComisDataGrowthBytes       = int64(1024 * 1024 * 1024)
	maximumComisDatabaseGrowthBytes   = int64(256 * 1024 * 1024)
	maximumComisDatabaseBytes         = int64(1024 * 1024 * 1024)
	maximumDevCrewDatabaseGrowthBytes = int64(128 * 1024 * 1024)
	maximumDevCrewDatabaseBytes       = int64(256 * 1024 * 1024)
)

// ServiceResourceSnapshot records bounded, content-free systemd process metrics.
type ServiceResourceSnapshot struct {
	Unit                string `json:"unit"`
	MainPID             uint64 `json:"mainPid"`
	MemoryBytes         int64  `json:"memoryBytes"`
	RSSBytes            int64  `json:"rssBytes"`
	Tasks               int    `json:"tasks"`
	OpenFileDescriptors int    `json:"openFileDescriptors"`
	JailProcesses       int    `json:"jailProcesses"`
}

// DataResourceSnapshot records regular-file counts and total bytes under one root.
type DataResourceSnapshot struct {
	RegularFiles int   `json:"regularFiles"`
	Bytes        int64 `json:"bytes"`
}

// ResourceSnapshot is one resource observation tied to the campaign clock.
type ResourceSnapshot struct {
	CapturedAtMs             int64                     `json:"capturedAtMs"`
	Services                 []ServiceResourceSnapshot `json:"services"`
	ComisData                DataResourceSnapshot      `json:"comisData"`
	ComisDatabaseBytes       int64                     `json:"comisDatabaseBytes"`
	DevCrewDatabaseBytes     int64                     `json:"devcrewDatabaseBytes"`
	WorktreeDirectories      int                       `json:"worktreeDirectories"`
	ActiveTerminalBindings   int                       `json:"activeTerminalBindings"`
	PendingDevCrewDeliveries int                       `json:"pendingDevcrewDeliveries"`
	UnsettledComisDeliveries int                       `json:"unsettledComisDeliveries"`
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
	ProcessRSSBytes(uint64) (int64, error)
	JailProcessCount(uint64) (int, error)
	ActiveTerminalCount(string) (int, error)
	PendingDevCrewDeliveryCount(string) (int, error)
	UnsettledComisDeliveryCount(string) (int, error)
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
	snapshot.ComisDatabaseBytes, err = reader.RegularFileBytes(manifest.Comis.DatabasePath)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect Comis database: %w", err)
	}
	snapshot.WorktreeDirectories, err = reader.DirectoryCount(manifest.DevCrew.WorktreeRoot)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect DevCrew worktree root: %w", err)
	}
	snapshot.ActiveTerminalBindings, err = reader.ActiveTerminalCount(manifest.DevCrew.DatabasePath)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect active terminal bindings: %w", err)
	}
	snapshot.PendingDevCrewDeliveries, err = reader.PendingDevCrewDeliveryCount(manifest.DevCrew.DatabasePath)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect DevCrew delivery backlog: %w", err)
	}
	snapshot.UnsettledComisDeliveries, err = reader.UnsettledComisDeliveryCount(manifest.Comis.DatabasePath)
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("capture resources: inspect Comis delivery backlog: %w", err)
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
	rssBytes, err := reader.ProcessRSSBytes(values["MainPID"])
	if err != nil || rssBytes <= 0 {
		return ServiceResourceSnapshot{}, fmt.Errorf("capture resources: inspect RSS for service %s", unit)
	}
	jails, err := reader.JailProcessCount(values["MainPID"])
	if err != nil || jails < 0 {
		return ServiceResourceSnapshot{}, fmt.Errorf("capture resources: inspect jail processes for service %s", unit)
	}
	return ServiceResourceSnapshot{
		Unit: unit, MainPID: values["MainPID"], MemoryBytes: int64(values["MemoryCurrent"]),
		RSSBytes: rssBytes, Tasks: int(values["TasksCurrent"]), OpenFileDescriptors: openFDs,
		JailProcesses: jails,
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
		if growth := finished[unit].RSSBytes - started[unit].RSSBytes; growth > maximumServiceRSSGrowthBytes {
			return fmt.Errorf("verify resource observation: service %s RSS growth exceeds the bounded allowance", unit)
		}
		if growth := finished[unit].OpenFileDescriptors - started[unit].OpenFileDescriptors; growth > maximumServiceOpenFDGrowth {
			return fmt.Errorf("verify resource observation: service %s file descriptor growth exceeds the bounded allowance", unit)
		}
	}
	if observation.Started.WorktreeDirectories != len(manifest.Tasks) || observation.Finished.WorktreeDirectories != 0 {
		return errors.New("verify resource observation: worktree counts do not prove two active lanes followed by complete cleanup")
	}
	if totalJailProcesses(observation.Started.Services) < len(manifest.Tasks) ||
		totalJailProcesses(observation.Finished.Services) != 0 {
		return errors.New("verify resource observation: jail process counts do not prove two active lanes followed by complete cleanup")
	}
	if observation.Started.ActiveTerminalBindings != len(manifest.Tasks) || observation.Finished.ActiveTerminalBindings != 0 {
		return errors.New("verify resource observation: terminal binding counts do not prove two active lanes followed by complete cleanup")
	}
	if observation.Started.PendingDevCrewDeliveries < 0 || observation.Started.UnsettledComisDeliveries < 0 ||
		observation.Finished.PendingDevCrewDeliveries != 0 || observation.Finished.UnsettledComisDeliveries != 0 {
		return errors.New("verify resource observation: delivery queues did not drain completely")
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
			service.RSSBytes <= 0 || service.Tasks <= 0 || service.OpenFileDescriptors <= 0 || service.JailProcesses < 0 {
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
	if observation.Started.ComisDatabaseBytes <= 0 || observation.Finished.ComisDatabaseBytes <= 0 ||
		observation.Finished.ComisDatabaseBytes > maximumComisDatabaseBytes ||
		observation.Finished.ComisDatabaseBytes-observation.Started.ComisDatabaseBytes > maximumComisDatabaseGrowthBytes {
		return errors.New("verify resource observation: Comis database size or growth exceeds the bounded allowance")
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

func totalJailProcesses(services []ServiceResourceSnapshot) int {
	total := 0
	for _, service := range services {
		total += service.JailProcesses
	}
	return total
}
