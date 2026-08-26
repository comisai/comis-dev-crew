package service

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/logging"
)

// TestRun_RecordsBoundaryCrossingsWhenALoggerIsComposed proves the wiring
// end to end, not just that the port exists.
//
// A logger constructed in the command and never threaded to a boundary is the
// failure this test exists for: everything compiles, the flag is accepted, and
// the service records nothing.
func TestRun_RecordsBoundaryCrossingsWhenALoggerIsComposed(t *testing.T) {
	root := shortTempDir(t)
	databasePath := filepath.Join(root, "state", "devcrew.db")
	socketPath := filepath.Join(root, "run", "devcrew.sock")

	var destination bytes.Buffer
	logger, err := logging.New(&destination, logging.LevelDebug)
	if err != nil {
		t.Fatalf("logging.New() error = %v", err)
	}

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: databasePath, SocketPath: socketPath,
			Clock: time.Now, Logger: logger, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}

	client, err := localapi.NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	if _, err := client.Fleet(context.Background(), "operation-boundary-log"); err != nil {
		t.Fatalf("Fleet() error = %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	var recorded map[string]any
	for _, line := range strings.Split(strings.TrimSpace(destination.String()), "\n") {
		if line == "" {
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("decode boundary line %q: %v", line, err)
		}
		if decoded["operationId"] == "operation-boundary-log" {
			recorded = decoded
		}
	}
	if recorded == nil {
		t.Fatalf("the served call recorded no boundary line; output %q", destination.String())
	}
	if recorded["boundary"] != string(application.BoundaryLocalAPI) {
		t.Errorf("boundary = %v, want the local API", recorded["boundary"])
	}
	if recorded["outcome"] != string(application.BoundaryCompleted) {
		t.Errorf("outcome = %v, want completed", recorded["outcome"])
	}
	if _, present := recorded["durationMs"]; !present {
		t.Errorf("record carried no duration: %v", recorded)
	}
}

// TestRun_ServesWithoutALogger keeps diagnostics optional at the composition
// root as well as at each boundary.
func TestRun_ServesWithoutALogger(t *testing.T) {
	root := shortTempDir(t)
	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			DatabasePath: filepath.Join(root, "state", "devcrew.db"),
			SocketPath:   filepath.Join(root, "run", "devcrew.sock"),
			Clock:        time.Now, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run() before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not advertise ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}
