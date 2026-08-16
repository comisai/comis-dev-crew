package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func (coordinator *runtimeAttachmentCoordinator) pinRuntimeAttachmentRelease(
	taskHandle string,
) (*pinnedTaskRuntimeDirectory, runtimeAttachmentIdentityRecord, error) {
	runtimeRootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		return nil, runtimeAttachmentIdentityRecord{}, err
	}
	record, _, found, err := readRuntimeAttachmentIdentityRecord(runtimeRootDescriptor, taskHandle)
	if err != nil || !found ||
		(record.Stage != runtimeAttachmentActive && record.Stage != runtimeAttachmentReleasing) {
		return nil, runtimeAttachmentIdentityRecord{}, errors.Join(
			errors.New("task runtime directory identity differs; path preserved"),
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	pinned, missing, err := openRecordedTaskRuntimeDirectory(runtimeRootDescriptor, taskHandle, record)
	if err != nil || missing {
		return nil, runtimeAttachmentIdentityRecord{}, errors.Join(
			errors.New("task runtime directory identity differs; path preserved"), err,
			closeRuntimeRootDescriptor(runtimeRootDescriptor),
		)
	}
	if record.Stage == runtimeAttachmentActive {
		staged := record
		staged.Stage = runtimeAttachmentReleasing
		if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, staged, &record, nil); err != nil {
			return nil, runtimeAttachmentIdentityRecord{}, errors.Join(
				errors.New("task runtime attachment release cannot be staged"), pinned.close(),
			)
		}
		record = staged
	}
	return pinned, record, nil
}

func preparePinnedRuntimeAttachmentClose(
	coordinator *runtimeAttachmentCoordinator,
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
	server *reporter.RuntimeServer,
) error {
	if err := isolatePinnedRuntimeAttachmentRelease(pinned, record); err != nil {
		return err
	}
	if err := server.RelocateSocket(filepath.Join(
		coordinator.runtimeRoot, pinned.directoryName, "attachment.sock",
	)); err != nil {
		return errors.New("task runtime attachment release socket cannot be isolated")
	}
	return nil
}

func isolatePinnedRuntimeAttachmentRelease(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) error {
	if pinned == nil || record.Stage != runtimeAttachmentReleasing {
		return errors.New("task runtime attachment release namespace is invalid")
	}
	releaseName := runtimeAttachmentReleaseName(pinned.taskHandle)
	if pinned.directoryName == releaseName {
		return nil
	}
	if pinned.directoryName != pinned.taskHandle {
		return errors.New("task runtime attachment release location is ambiguous")
	}
	if err := reporter.PublishRuntimeDirectory(
		pinned.runtimeRootDescriptor, pinned.directoryName, releaseName,
		pinned.taskIdentity, 0o700,
	); err != nil {
		return errors.New("task runtime attachment release cannot be isolated")
	}
	pinned.directoryName = releaseName
	return nil
}

func runtimeAttachmentReleaseName(taskHandle string) string {
	digest := sha256.Sum256([]byte(taskHandle))
	return fmt.Sprintf(".dr-%x", digest[:8])
}
