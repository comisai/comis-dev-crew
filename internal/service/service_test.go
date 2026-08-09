package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestRun_ServesPersistedQueriesAndRestartsCleanly(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	seed, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	task := serviceTask()
	if err := seed.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	for iteration := 1; iteration <= 2; iteration++ {
		ready := make(chan struct{})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			done <- Run(ctx, Config{
				DatabasePath: databasePath,
				SocketPath:   socketPath,
				Ready:        func() { close(ready) },
			})
		}()
		<-ready

		client, err := localapi.NewClient(socketPath, time.Second)
		if err != nil {
			t.Fatalf("iteration %d NewClient() error = %v", iteration, err)
		}
		detail, err := client.ShowTask(context.Background(), "read-0001", task.Handle)
		if err != nil {
			t.Fatalf("iteration %d ShowTask() error = %v", iteration, err)
		}
		if detail.Summary.TaskHandle != task.Handle || detail.StateVersion != task.StateVersion {
			t.Fatalf("iteration %d detail = %#v, want persisted task", iteration, detail)
		}

		cancel()
		if err := <-done; err != nil {
			t.Fatalf("iteration %d Run() error = %v", iteration, err)
		}
		if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
			t.Fatalf("iteration %d socket still exists: %v", iteration, err)
		}
	}
}

func TestRun_RejectsMissingContextAndConfiguration(t *testing.T) {
	valid := Config{
		DatabasePath: filepath.Join(shortTempDir(t), "state", "devcrew.db"),
		SocketPath:   filepath.Join(shortTempDir(t), "run", "devcrew.sock"),
	}
	//lint:ignore SA1012 The boundary test proves service composition rejects nil context.
	if err := Run(nil, valid); err == nil {
		t.Fatal("Run(nil context) error = nil")
	}
	if err := Run(context.Background(), Config{}); err == nil {
		t.Fatal("Run(empty config) error = nil")
	}
	if err := Run(context.Background(), Config{DatabasePath: "relative.db", SocketPath: valid.SocketPath}); err == nil {
		t.Fatal("Run(relative database) error = nil")
	}
	if err := Run(context.Background(), Config{DatabasePath: valid.DatabasePath, SocketPath: "relative.sock"}); err == nil {
		t.Fatal("Run(relative socket) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v, want context.Canceled", err)
	}
}

func TestRun_ReconcilesAmbiguousStateBeforeAdvertisingReady(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")
	seed, err := sqlite.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatalf("open seed store: %v", err)
	}
	task := serviceTask()
	task.ManagedRunID = "managed-run-0001"
	task.WorkspaceLeaseID = "workspace-lease-0001"
	task.State = domain.TaskWorking
	if err := seed.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("seed working task: %v", err)
	}
	accepted := domain.OperationRecord{
		SchemaVersion: 1, ID: "op-accepted-0001", Command: "StartTask",
		SubjectDigest: strings.Repeat("b", 64), Status: domain.OperationAccepted,
		StateVersion: 2, CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt,
	}
	if err := seed.RecordOperation(context.Background(), accepted); err != nil {
		t.Fatalf("seed accepted operation: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: socketPath,
			Clock: time.Now, Ready: func() { close(ready) },
		})
	}()
	<-ready
	client, err := localapi.NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	detail, err := client.ShowTask(context.Background(), "read-reconciled-task", task.Handle)
	if err != nil || detail.Summary.State != domain.TaskUnknown {
		t.Fatalf("ShowTask() = %#v, %v, want unknown before ready", detail, err)
	}
	operation, err := client.Operation(context.Background(), "read-reconciled-operation", accepted.ID)
	if err != nil || operation.Status != domain.OperationUnknown {
		t.Fatalf("Operation() = %#v, %v, want unknown before ready", operation, err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func serviceTask() domain.Task {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	task := domain.Task{
		SchemaVersion:      1,
		Handle:             "task-0001",
		ServiceInstanceID:  "service-instance-0001",
		State:              domain.TaskPrepared,
		Shape:              domain.ShapeShip,
		RepositoryID:       "product-api",
		BaseRevision:       strings.Repeat("a", 40),
		BriefRevision:      1,
		AcceptanceCriteria: []string{"The requested change is proven."},
		Constraints:        []string{"Preserve unrelated changes."},
		ValidationProfile:  "go-default",
		DeliveryMode:       domain.DeliveryPullRequest,
		WorkerProfileID:    "codex-standard",
		StateVersion:       1,
		CreatedAt:          created,
		UpdatedAt:          created,
	}
	pinned, err := task.PinBriefRevision()
	if err != nil {
		panic(err)
	}
	return pinned
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp(os.TempDir(), "devcrew-service-")
	if err != nil {
		t.Fatalf("create short temporary directory: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove short temporary directory: %v", err)
		}
	})
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve short temporary directory: %v", err)
	}
	return resolved
}
