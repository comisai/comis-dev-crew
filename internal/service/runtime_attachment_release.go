package service

import (
	"context"
	"errors"

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
	var pinned *pinnedTaskRuntimeDirectory
	var record runtimeAttachmentIdentityRecord
	if entry != nil {
		var pinErr error
		pinned, record, pinErr = coordinator.pinRuntimeAttachmentRelease(taskHandle)
		if pinErr != nil {
			coordinator.mu.Unlock()
			return errors.New("release runtime attachment: task runtime directory identity is unavailable")
		}
	}
	delete(coordinator.entries, taskHandle)
	coordinator.mu.Unlock()
	if entry != nil {
		if err := entry.server.Close(); err != nil {
			return errors.Join(err, pinned.close())
		}
		removeErr := removePinnedRuntimeAttachment(pinned, record)
		if err := errors.Join(removeErr, pinned.close()); err != nil {
			return errors.New("release runtime attachment: task runtime directory is not empty or unavailable")
		}
		return nil
	}
	if err := coordinator.removeTaskRuntimeDirectory(taskHandle); err != nil {
		return errors.New("release runtime attachment: task runtime directory is not empty or unavailable")
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

func closeRuntimeServers(servers []*reporter.RuntimeServer) error {
	var resultErr error
	for _, server := range servers {
		resultErr = errors.Join(resultErr, server.Close())
	}
	return resultErr
}
