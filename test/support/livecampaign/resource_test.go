package livecampaign

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type resourceReaderFixture struct {
	databaseBytes int64
	dataFiles     int
	dataBytes     int64
	worktrees     int
	openFDs       map[uint64]int
	rssBytes      map[uint64]int64
	jails         map[uint64]int
	terminals     int
	devDeliveries int
	comisDelivery int
}

func (reader resourceReaderFixture) RegularFileBytes(string) (int64, error) {
	return reader.databaseBytes, nil
}

func (reader resourceReaderFixture) TreeTotals(string) (int, int64, error) {
	return reader.dataFiles, reader.dataBytes, nil
}

func (reader resourceReaderFixture) DirectoryCount(string) (int, error) {
	return reader.worktrees, nil
}

func (reader resourceReaderFixture) OpenFDCount(pid uint64) (int, error) {
	count, exists := reader.openFDs[pid]
	if !exists {
		return 0, errors.New("missing pid")
	}
	return count, nil
}

func (reader resourceReaderFixture) ProcessRSSBytes(pid uint64) (int64, error) {
	value, exists := reader.rssBytes[pid]
	if !exists {
		return 0, errors.New("missing pid")
	}
	return value, nil
}

func (reader resourceReaderFixture) JailProcessCount(pid uint64) (int, error) {
	return reader.jails[pid], nil
}

func (reader resourceReaderFixture) ActiveTerminalCount(string) (int, error) {
	return reader.terminals, nil
}

func (reader resourceReaderFixture) PendingDevCrewDeliveryCount(string) (int, error) {
	return reader.devDeliveries, nil
}

func (reader resourceReaderFixture) UnsettledComisDeliveryCount(string) (int, error) {
	return reader.comisDelivery, nil
}

type resourceExecutorFixture struct {
	manifest Manifest
}

func (executor resourceExecutorFixture) Run(_ context.Context, command Command) ([]byte, error) {
	if command.Path != executor.manifest.Services.SystemctlPath || len(command.Args) != 5 ||
		command.Args[0] != "show" || command.Args[2] != "--property" || command.Args[3] == "" ||
		command.Args[4] != "--value" {
		return nil, errors.New("unexpected command")
	}
	unit := command.Args[1]
	base := uint64(100)
	switch unit {
	case executor.manifest.Services.MCPUnit:
		base = 200
	case executor.manifest.Services.DevCrewUnit:
		base = 300
	case executor.manifest.Services.ComisUnit:
		base = 400
	default:
		return nil, errors.New("unexpected unit")
	}
	switch command.Args[3] {
	case "MainPID":
		return []byte(strconv.FormatUint(base, 10) + "\n"), nil
	case "MemoryCurrent":
		return []byte(strconv.FormatUint(base*1024, 10) + "\n"), nil
	case "TasksCurrent":
		return []byte("4\n"), nil
	default:
		return nil, errors.New("unexpected property")
	}
}

func TestCaptureResourceSnapshotReadsExactServicesAndRoots(t *testing.T) {
	manifest := validManifest()
	reader := resourceReaderFixture{
		databaseBytes: 4096, dataFiles: 12, dataBytes: 8192, worktrees: 2,
		openFDs:  map[uint64]int{200: 10, 300: 20, 400: 30},
		rssBytes: map[uint64]int64{200: 1024, 300: 2048, 400: 4096},
		jails:    map[uint64]int{400: 2}, terminals: 2, devDeliveries: 1, comisDelivery: 1,
	}
	snapshot, err := captureResourceSnapshot(
		context.Background(), manifest, resourceExecutorFixture{manifest: manifest}, reader,
		manifest.StartedAtMs+1,
	)
	if err != nil {
		t.Fatalf("captureResourceSnapshot() error = %v", err)
	}
	if len(snapshot.Services) != 3 || snapshot.Services[1].MainPID != 300 ||
		snapshot.Services[2].OpenFileDescriptors != 30 || snapshot.Services[2].RSSBytes != 4096 ||
		snapshot.Services[2].JailProcesses != 2 || snapshot.ComisData.RegularFiles != 12 ||
		snapshot.DevCrewDatabaseBytes != 4096 || snapshot.ComisDatabaseBytes != 4096 ||
		snapshot.WorktreeDirectories != 2 ||
		snapshot.ActiveTerminalBindings != 2 || snapshot.PendingDevCrewDeliveries != 1 ||
		snapshot.UnsettledComisDeliveries != 1 {
		t.Fatalf("resource snapshot = %#v", snapshot)
	}
}

func TestVerifyResourceObservationRequiresOneHourAndCleanResourceFinish(t *testing.T) {
	manifest := validManifest()
	observation := validResourceObservation(manifest)
	if err := VerifyResourceObservation(manifest, observation); err != nil {
		t.Fatalf("VerifyResourceObservation(valid) error = %v", err)
	}

	short := observation
	short.Finished.CapturedAtMs = short.Started.CapturedAtMs + 3_599_999
	if err := VerifyResourceObservation(manifest, short); err == nil || !strings.Contains(err.Error(), "one hour") {
		t.Fatalf("expected duration refusal, got %v", err)
	}

	residual := observation
	residual.Finished.WorktreeDirectories = 1
	if err := VerifyResourceObservation(manifest, residual); err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("expected residual-worktree refusal, got %v", err)
	}

	terminal := validResourceObservation(manifest)
	terminal.Finished.ActiveTerminalBindings = 1
	if err := VerifyResourceObservation(manifest, terminal); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("expected residual-terminal refusal, got %v", err)
	}

	queue := validResourceObservation(manifest)
	queue.Finished.UnsettledComisDeliveries = 1
	if err := VerifyResourceObservation(manifest, queue); err == nil || !strings.Contains(err.Error(), "delivery") {
		t.Fatalf("expected residual-delivery refusal, got %v", err)
	}

	growth := observation
	growth.Finished.Services[0].MemoryBytes += maximumServiceMemoryGrowthBytes + 1
	if err := VerifyResourceObservation(manifest, growth); err == nil || !strings.Contains(err.Error(), "memory") {
		t.Fatalf("expected memory-growth refusal, got %v", err)
	}
}

func validResourceObservation(manifest Manifest) ResourceObservation {
	service := func(unit string, pid uint64) ServiceResourceSnapshot {
		return ServiceResourceSnapshot{
			Unit: unit, MainPID: pid, MemoryBytes: 64 * 1024 * 1024,
			RSSBytes: 32 * 1024 * 1024, Tasks: 4, OpenFileDescriptors: 20,
		}
	}
	started := ResourceSnapshot{
		CapturedAtMs: manifest.StartedAtMs, Services: []ServiceResourceSnapshot{
			service(manifest.Services.MCPUnit, 200), service(manifest.Services.DevCrewUnit, 300),
			service(manifest.Services.ComisUnit, 400),
		},
		ComisData:            DataResourceSnapshot{RegularFiles: 10, Bytes: 8192},
		DevCrewDatabaseBytes: 4096, ComisDatabaseBytes: 4096,
		WorktreeDirectories:    len(manifest.Tasks),
		ActiveTerminalBindings: len(manifest.Tasks),
	}
	started.Services[2].JailProcesses = len(manifest.Tasks)
	finished := started
	finished.CapturedAtMs = manifest.EndedAtMs
	finished.Services = append([]ServiceResourceSnapshot(nil), started.Services...)
	finished.ComisData.Bytes += 4096
	finished.DevCrewDatabaseBytes += 4096
	finished.ComisDatabaseBytes += 4096
	finished.WorktreeDirectories = 0
	finished.ActiveTerminalBindings = 0
	finished.Services[2].JailProcesses = 0
	return ResourceObservation{SchemaVersion: 1, Started: started, Finished: finished}
}

func TestRealResourceReaderCountsTerminalAndDeliveryBacklogs(t *testing.T) {
	root := t.TempDir()
	devCrewPath := filepath.Join(root, "devcrew.db")
	comisPath := filepath.Join(root, "comis.db")
	createCountFixtureDatabase(t, devCrewPath, []string{
		"CREATE TABLE task_terminal_bindings (latest_transition TEXT NOT NULL)",
		"INSERT INTO task_terminal_bindings VALUES ('running'), ('exited')",
		"CREATE TABLE comis_report_outbox (delivered_at TEXT)",
		"INSERT INTO comis_report_outbox VALUES (NULL), ('done')",
	})
	createCountFixtureDatabase(t, comisPath, []string{
		"CREATE TABLE delivery_queue (status TEXT NOT NULL)",
		"INSERT INTO delivery_queue VALUES ('pending'), ('delivered')",
	})
	reader := realResourceReader{}
	terminals, terminalErr := reader.ActiveTerminalCount(devCrewPath)
	devDeliveries, devErr := reader.PendingDevCrewDeliveryCount(devCrewPath)
	comisDeliveries, comisErr := reader.UnsettledComisDeliveryCount(comisPath)
	if terminalErr != nil || devErr != nil || comisErr != nil || terminals != 1 || devDeliveries != 1 || comisDeliveries != 1 {
		t.Fatalf("resource counts = terminals:%d dev:%d comis:%d errors:%v/%v/%v", terminals, devDeliveries, comisDeliveries, terminalErr, devErr, comisErr)
	}
}

func createCountFixtureDatabase(t *testing.T, path string, statements []string) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range statements {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}
