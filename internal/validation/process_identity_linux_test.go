//go:build linux

package validation

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestObserveOSProcessPreservesExitedReviewedProgramIdentity(t *testing.T) {
	command := exec.Command("/usr/bin/true")
	if err := command.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = command.Wait() })

	statPath := filepath.Join("/proc", strconv.Itoa(command.Process.Pid), "stat")
	deadline := time.Now().Add(5 * time.Second)
	for {
		stat, err := os.ReadFile(statPath)
		if err != nil {
			t.Fatalf("ReadFile(proc stat) error = %v", err)
		}
		closing := strings.LastIndexByte(string(stat), ')')
		if closing >= 0 && strings.HasPrefix(string(stat[closing+1:]), " Z ") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reviewed program did not become an unreaped zombie")
		}
		time.Sleep(time.Millisecond)
	}

	observation, err := observeOSProcess(context.Background(), command.Process.Pid)
	if err != nil {
		t.Fatalf("observeOSProcess(exited reviewed program) error = %v", err)
	}
	if observation.PID != command.Process.Pid || observation.StartIdentity == "" ||
		observation.ProcessGroupIdentity == "" || observation.ExecutableLabel != "true" || !observation.Exited {
		t.Fatalf("observeOSProcess(exited reviewed program) = %#v", observation)
	}
}
