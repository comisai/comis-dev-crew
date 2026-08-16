package service

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

type runtimeAttachmentEntryState uint8

const (
	runtimeAttachmentEntryReady runtimeAttachmentEntryState = iota
	runtimeAttachmentEntryPending
	runtimeAttachmentEntryReleasing
)

type runtimeAttachmentEntry struct {
	request          application.RuntimeAttachmentPreparationRequest
	attachment       application.PreparedRuntimeAttachment
	server           *reporter.RuntimeServer
	binding          *application.RuntimeAttachmentBindingRequest
	state            runtimeAttachmentEntryState
	registrationDone chan struct{}
	registrationErr  error
	releaseDone      chan struct{}
	releaseErr       error
}

func (coordinator *runtimeAttachmentCoordinator) completeRuntimeAttachmentRegistration(
	taskHandle string,
	entry *runtimeAttachmentEntry,
	resultErr error,
) {
	coordinator.mu.Lock()
	entry.registrationErr = resultErr
	entry.state = runtimeAttachmentEntryReady
	if resultErr != nil && coordinator.entries[taskHandle] == entry {
		delete(coordinator.entries, taskHandle)
	}
	close(entry.registrationDone)
	coordinator.mu.Unlock()
}

func (coordinator *runtimeAttachmentCoordinator) completeRuntimeAttachmentRelease(
	taskHandle string,
	entry *runtimeAttachmentEntry,
	resultErr error,
) {
	coordinator.mu.Lock()
	entry.releaseErr = resultErr
	if coordinator.entries[taskHandle] == entry {
		delete(coordinator.entries, taskHandle)
	}
	close(entry.releaseDone)
	coordinator.mu.Unlock()
}

func runtimeAttachmentRegistrationResult(
	entry *runtimeAttachmentEntry,
) (application.PreparedRuntimeAttachment, error) {
	if entry.registrationErr != nil {
		return application.PreparedRuntimeAttachment{}, entry.registrationErr
	}
	return entry.attachment, nil
}

func runtimeAttachmentReleaseResult(entry *runtimeAttachmentEntry) error {
	return entry.releaseErr
}

func runtimeAttachmentEntryUnavailable(state runtimeAttachmentEntryState) error {
	if state == runtimeAttachmentEntryReleasing {
		return errors.New("runtime attachment entry is releasing")
	}
	return errors.New("runtime attachment entry is unavailable")
}

func (coordinator *runtimeAttachmentCoordinator) waitRuntimeAttachmentReplay(
	ctx context.Context,
	done <-chan struct{},
	stopped string,
) error {
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-coordinator.runDone:
		select {
		case <-done:
			return nil
		default:
			return errors.New(stopped)
		}
	}
}

func (coordinator *runtimeAttachmentCoordinator) releaseFailedRuntimeAttachmentRegistration(
	entry *runtimeAttachmentEntry,
) error {
	pinned, record, err := coordinator.pinRuntimeAttachmentRelease(entry.request.TaskHandle)
	if err != nil {
		return errors.Join(err, entry.server.Close())
	}
	record, err = preparePinnedRuntimeAttachmentClose(coordinator, pinned, record, entry.server)
	if err != nil {
		return errors.Join(err, entry.server.Close(), pinned.close())
	}
	if err := entry.server.Close(); err != nil {
		return errors.Join(err, pinned.close())
	}
	return errors.Join(removePinnedRuntimeAttachment(pinned, record), pinned.close())
}
