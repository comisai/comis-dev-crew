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
		record, pinErr = preparePinnedRuntimeAttachmentClose(coordinator, pinned, record, entry.server)
		if pinErr != nil {
			coordinator.mu.Unlock()
			return errors.Join(pinErr, pinned.close())
		}
	}
	delete(coordinator.entries, taskHandle)
	coordinator.mu.Unlock()
	if entry != nil {
		if err := coordinator.releaseRuntimeServer(entry.server); err != nil {
			return errors.Join(err, entry.server.Close(), pinned.close())
		}
		if err := entry.server.Close(); err != nil {
			return errors.Join(err, pinned.close())
		}
		if coordinator.afterRuntimeAttachmentClose != nil {
			if err := coordinator.afterRuntimeAttachmentClose(); err != nil {
				return errors.Join(err, pinned.close())
			}
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

func (coordinator *runtimeAttachmentCoordinator) releaseRuntimeServer(server *reporter.RuntimeServer) error {
	release := runtimeAttachmentRelease{server: server, ready: make(chan error, 1)}
	select {
	case coordinator.releases <- release:
	case <-coordinator.runDone:
		return errors.New("release runtime attachment: coordinator stopped")
	}
	select {
	case err := <-release.ready:
		return err
	case <-coordinator.runDone:
		return errors.New("release runtime attachment: coordinator stopped")
	}
}

func closeRuntimeServers(servers []*reporter.RuntimeServer) error {
	var resultErr error
	for _, server := range servers {
		resultErr = errors.Join(resultErr, server.Close())
	}
	return resultErr
}
