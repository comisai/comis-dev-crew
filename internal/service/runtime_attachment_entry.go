package service

import (
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
