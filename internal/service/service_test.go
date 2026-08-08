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
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Run(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run(cancelled) error = %v, want context.Canceled", err)
	}
}

func serviceTask() domain.Task {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	return domain.Task{
		SchemaVersion:     1,
		Handle:            "task-0001",
		State:             domain.TaskPrepared,
		Shape:             domain.ShapeShip,
		RepositoryID:      "product-api",
		BaseRevision:      strings.Repeat("a", 40),
		BriefRevision:     1,
		ValidationProfile: "go-default",
		DeliveryMode:      domain.DeliveryPullRequest,
		WorkerProfileID:   "codex-standard",
		StateVersion:      1,
		CreatedAt:         created,
		UpdatedAt:         created,
	}
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
