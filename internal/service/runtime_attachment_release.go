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
		if _, refused := coordinator.runtimeAttachmentRefusals[taskHandle]; refused {
			coordinator.mu.Unlock()
			return errors.New("release runtime attachment: filesystem ownership is unproven")
		}
		entry := coordinator.entries[taskHandle]
		if entry == nil {
			coordinator.mu.Unlock()
			if err := coordinator.removeTaskRuntimeDirectory(taskHandle); err != nil {
				if errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
					return errors.Join(
						errors.New("release runtime attachment: filesystem ownership is unproven"),
						coordinator.recordRuntimeAttachmentTaskRefusal(ctx, taskHandle),
					)
				}
				return errors.New("release runtime attachment: task runtime directory is not empty or unavailable")
			}
			return nil
		}
		switch entry.state {
		case runtimeAttachmentEntryPending:
			done := entry.registrationDone
			observed := coordinator.runtimeAttachmentReleaseReplayObserved
			coordinator.mu.Unlock()
			if observed != nil {
				observed()
			}
			if err := coordinator.waitRuntimeAttachmentReplay(
				ctx, done, "release runtime attachment: coordinator stopped",
			); err != nil {
				return err
			}
			continue
		case runtimeAttachmentEntryReleasing:
			done := entry.releaseDone
			observed := coordinator.runtimeAttachmentReleaseReplayObserved
			coordinator.mu.Unlock()
			if observed != nil {
				observed()
			}
			if err := coordinator.waitRuntimeAttachmentReplay(
				ctx, done, "release runtime attachment: coordinator stopped",
			); err != nil {
				return err
			}
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
		if pinErr == nil {
			record, pinErr = preparePinnedRuntimeAttachmentClose(coordinator, pinned, record, entry.server)
		}
		entry.state = runtimeAttachmentEntryReleasing
		entry.releaseDone = make(chan struct{})
		coordinator.mu.Unlock()
		if pinErr != nil {
			resultErr := coordinator.releaseUnprovenRuntimeAttachment(ctx, taskHandle, entry, pinned, pinErr)
			coordinator.completeRuntimeAttachmentRelease(taskHandle, entry, resultErr)
			return resultErr
		}
		resultErr := coordinator.releaseRegisteredRuntimeAttachment(entry, pinned, record)
		coordinator.completeRuntimeAttachmentRelease(taskHandle, entry, resultErr)
		return resultErr
	}
}

func (coordinator *runtimeAttachmentCoordinator) releaseUnprovenRuntimeAttachment(
	ctx context.Context,
	taskHandle string,
	entry *runtimeAttachmentEntry,
	pinned *pinnedTaskRuntimeDirectory,
	proofErr error,
) error {
	releaseErr := coordinator.releaseRuntimeServer(entry.server)
	closeErr := entry.server.Close()
	var pinCloseErr error
	if pinned != nil {
		pinCloseErr = pinned.close()
	}
	refusalErr := coordinator.recordRuntimeAttachmentTaskRefusal(ctx, taskHandle)
	return errors.Join(
		errors.New("release runtime attachment: filesystem ownership is unproven"),
		proofErr,
		releaseErr,
		closeErr,
		pinCloseErr,
		refusalErr,
	)
}

func (coordinator *runtimeAttachmentCoordinator) recordRuntimeAttachmentTaskRefusal(
	ctx context.Context,
	taskHandle string,
) error {
	if err := coordinator.persistRuntimeAttachmentTaskRefusal(context.WithoutCancel(ctx), taskHandle); err != nil {
		return err
	}
	coordinator.mu.Lock()
	coordinator.runtimeAttachmentRefusals[taskHandle] = struct{}{}
	coordinator.mu.Unlock()
	return nil
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
	if closeErr := pinned.close(); removeErr != nil || closeErr != nil {
		return errors.Join(
			errors.New("release runtime attachment: task runtime directory is not empty or unavailable"),
			removeErr,
			closeErr,
		)
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
