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
		(record.Stage != runtimeAttachmentActive && record.Stage != runtimeAttachmentReleaseIntent &&
			record.Stage != runtimeAttachmentReleasing) {
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
		staged.Stage = runtimeAttachmentReleaseIntent
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
) (runtimeAttachmentIdentityRecord, error) {
	updated, err := isolatePinnedRuntimeAttachmentRelease(pinned, record)
	if err != nil {
		return runtimeAttachmentIdentityRecord{}, err
	}
	if err := server.RelocateSocket(filepath.Join(
		coordinator.runtimeRoot, pinned.directoryName, "attachment.sock",
	)); err != nil {
		return runtimeAttachmentIdentityRecord{}, errors.New("task runtime attachment release socket cannot be isolated")
	}
	return updated, nil
}

func isolatePinnedRuntimeAttachmentRelease(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) (runtimeAttachmentIdentityRecord, error) {
	if pinned == nil || (record.Stage != runtimeAttachmentReleaseIntent && record.Stage != runtimeAttachmentReleasing) {
		return runtimeAttachmentIdentityRecord{}, errors.New("task runtime attachment release namespace is invalid")
	}
	releaseName := runtimeAttachmentReleaseName(pinned.taskHandle)
	if pinned.directoryName != releaseName && pinned.directoryName != pinned.taskHandle {
		return runtimeAttachmentIdentityRecord{}, errors.New("task runtime attachment release location is ambiguous")
	}
	if pinned.directoryName == releaseName && record.Stage == runtimeAttachmentReleasing {
		return record, nil
	}
	current := pinned.taskIdentity
	if pinned.directoryName != releaseName {
		var err error
		current, err = reporter.PublishRuntimeDirectoryIdentity(
			pinned.runtimeRootDescriptor, pinned.directoryName, releaseName, pinned.taskIdentity, 0o700,
		)
		if err != nil {
			return runtimeAttachmentIdentityRecord{}, errors.New("task runtime attachment release cannot be isolated")
		}
		pinned.directoryName = releaseName
	} else {
		var err error
		current, err = runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
		if err != nil || !sameRuntimeAttachmentNode(current, record.Task) ||
			!runtimeAttachmentSocketMatches(pinned.taskDescriptor, record.Socket) {
			return runtimeAttachmentIdentityRecord{}, errors.New("task runtime attachment release identity differs")
		}
	}
	staged := record
	staged.Stage = runtimeAttachmentReleasing
	staged.Task = current
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, staged, &record, nil); err != nil {
		return runtimeAttachmentIdentityRecord{}, errors.New("task runtime attachment release identity cannot be bound")
	}
	pinned.taskIdentity = current
	return staged, nil
}

func runtimeAttachmentReleaseName(taskHandle string) string {
	digest := sha256.Sum256([]byte(taskHandle))
	return fmt.Sprintf(".dr-%x", digest[:8])
}
