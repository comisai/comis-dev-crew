package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func (coordinator *runtimeAttachmentCoordinator) ReleaseRuntimeAttachment(ctx context.Context, taskHandle string) error {
	if ctx == nil {
		return errors.New("release runtime attachment: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return errors.New("release runtime attachment: task handle is invalid")
	}
	select {
	case <-coordinator.recoveryReady:
	case <-ctx.Done():
		return ctx.Err()
	}
	coordinator.mu.Lock()
	if coordinator.recoveryErr != nil {
		recoveryErr := coordinator.recoveryErr
		coordinator.mu.Unlock()
		return recoveryErr
	}
	entry := coordinator.entries[taskHandle]
	delete(coordinator.entries, taskHandle)
	coordinator.mu.Unlock()
	if entry != nil {
		if err := entry.server.Close(); err != nil {
			return err
		}
	}
	if err := coordinator.removeTaskRuntimeDirectory(taskHandle); err != nil {
		return errors.New("release runtime attachment: task runtime directory is not empty or unavailable")
	}
	return nil
}

func (coordinator *runtimeAttachmentCoordinator) removeTaskRuntimeDirectory(taskHandle string) error {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return errors.New("task runtime directory identity is invalid")
	}
	taskRoot := filepath.Join(coordinator.runtimeRoot, taskHandle)
	info, err := os.Lstat(taskRoot)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("task runtime directory is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(taskRoot)
	if err != nil || resolved != taskRoot {
		return errors.New("task runtime directory is not canonical")
	}
	socketPath := filepath.Join(taskRoot, "attachment.sock")
	socketInfo, err := os.Lstat(socketPath)
	if err == nil {
		if socketInfo.Mode()&os.ModeSocket == 0 || socketInfo.Mode().Perm() != 0o600 {
			return errors.New("task runtime directory contains an unsafe attachment")
		}
		if err := os.Remove(socketPath); err != nil {
			return errors.New("task runtime attachment cannot be removed")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("task runtime attachment is unavailable")
	}
	if err := os.Remove(taskRoot); err != nil && !os.IsNotExist(err) {
		return errors.New("task runtime directory is not empty or unavailable")
	}
	return nil
}

func (coordinator *runtimeAttachmentCoordinator) hasRuntimeServer(server *reporter.RuntimeServer) bool {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	for _, entry := range coordinator.entries {
		if entry.server == server {
			return true
		}
	}
	return false
}
