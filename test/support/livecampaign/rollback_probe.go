package livecampaign

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// RealRollbackServiceProbe starts the previous service on copied state and reads it through the previous CLI.
func RealRollbackServiceProbe(
	ctx context.Context,
	servicePath string,
	cliPath string,
	databasePath string,
	socketPath string,
) error {
	if ctx == nil || validateExecutable(servicePath) != nil || validateExecutable(cliPath) != nil ||
		validateCanonicalRegularFile(databasePath) != nil {
		return errors.New("rollback service probe inputs are invalid")
	}
	if _, err := os.Lstat(socketPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("rollback service probe socket must not exist")
	}
	command := exec.CommandContext(ctx, servicePath, "--database", databasePath, "--socket", socketPath)
	environment, err := protectedCommandEnvironment(nil, false)
	if err != nil {
		return errors.New("rollback service probe environment is unavailable")
	}
	command.Env = environment
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return errors.New("rollback service probe could not start the previous service")
	}
	defer stopRollbackProbe(command)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return errors.New("rollback service probe context ended before readiness")
		case <-deadline.C:
			return errors.New("rollback service probe timed out before readiness")
		case <-ticker.C:
			info, err := os.Lstat(socketPath)
			if err == nil && info.Mode()&os.ModeSocket != 0 && info.Mode().Perm() == 0o600 {
				output, err := (RealExecutor{}).Run(ctx, Command{
					Path: cliPath, Args: []string{"--socket", socketPath, "service", "status"},
				})
				if err != nil || !strings.Contains(string(output), "healthy") || !strings.Contains(string(output), "complete") {
					return errors.New("rollback service probe status is not healthy and complete")
				}
				return nil
			}
		}
	}
}

func stopRollbackProbe(command *exec.Cmd) {
	if command.Process == nil {
		return
	}
	_ = command.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = command.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-done
	}
}
