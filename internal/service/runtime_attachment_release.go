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
	for {
		coordinator.mu.Lock()
		if coordinator.recoveryErr != nil {
			recoveryErr := coordinator.recoveryErr
			coordinator.mu.Unlock()
			return recoveryErr
		}
		if _, refused := coordinator.runtimeRelayIdentityRefusals[taskHandle]; refused {
			coordinator.mu.Unlock()
			return errors.New("release runtime attachment: relay ownership is unproven")
		}
		entry := coordinator.entries[taskHandle]
		if entry == nil {
			coordinator.mu.Unlock()
			if err := coordinator.removeTaskRuntimeDirectory(taskHandle); err != nil {
				return errors.New("release runtime attachment: task runtime directory is not empty or unavailable")
			}
			return nil
		}
		switch entry.state {
		case runtimeAttachmentEntryPending:
			done := entry.registrationDone
			coordinator.mu.Unlock()
			<-done
			continue
		case runtimeAttachmentEntryReleasing:
			done := entry.releaseDone
			observed := coordinator.runtimeAttachmentReleaseReplayObserved
			coordinator.mu.Unlock()
			if observed != nil {
				observed()
			}
			<-done
			coordinator.mu.Lock()
			resultErr := runtimeAttachmentReleaseResult(entry)
			coordinator.mu.Unlock()
			return resultErr
		case runtimeAttachmentEntryReady:
		default:
			coordinator.mu.Unlock()
			return errors.New("release runtime attachment: task attachment state is invalid")
		}
		pinned, record, pinErr := coordinator.pinRuntimeAttachmentRelease(taskHandle)
		if pinErr != nil {
			coordinator.mu.Unlock()
			return errors.New("release runtime attachment: task runtime directory identity is unavailable")
		}
		record, pinErr = preparePinnedRuntimeAttachmentClose(coordinator, pinned, record, entry.server)
		if pinErr != nil {
			coordinator.mu.Unlock()
			return errors.Join(pinErr, pinned.close())
		}
		entry.state = runtimeAttachmentEntryReleasing
		entry.releaseDone = make(chan struct{})
		coordinator.mu.Unlock()
		resultErr := coordinator.releaseRegisteredRuntimeAttachment(entry, pinned, record)
		coordinator.completeRuntimeAttachmentRelease(taskHandle, entry, resultErr)
		return resultErr
	}
}

func (coordinator *runtimeAttachmentCoordinator) releaseRegisteredRuntimeAttachment(
	entry *runtimeAttachmentEntry,
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) error {
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
