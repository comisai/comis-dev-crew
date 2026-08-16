package service

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

var errRuntimeAttachmentPreparationUnproven = errors.New("runtime attachment preparation authority is unproven")

func (coordinator *runtimeAttachmentCoordinator) removeTaskRuntimeDirectory(taskHandle string) error {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return errors.New("task runtime directory identity is invalid")
	}
	runtimeRootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		return err
	}
	record, _, identityFound, err := readRuntimeAttachmentIdentityRecord(runtimeRootDescriptor, taskHandle)
	if err != nil {
		return errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	if !identityFound {
		if !runtimeAttachmentPathAbsent(runtimeRootDescriptor, taskHandle) ||
			!runtimeAttachmentPathAbsent(runtimeRootDescriptor, runtimeAttachmentCreationName(taskHandle)) {
			return errors.Join(
				errRuntimeAttachmentPreparationUnproven,
				errors.New("task runtime directory identity is unproven; path preserved"),
				closeRuntimeRootDescriptor(runtimeRootDescriptor),
			)
		}
		return closeRuntimeRootDescriptor(runtimeRootDescriptor)
	}
	if record.Stage == runtimeAttachmentCreatingIntent {
		resultErr := removeRuntimeAttachmentCreationIntent(runtimeRootDescriptor, taskHandle, record)
		return errors.Join(resultErr, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	pinned, missing, err := openRecordedTaskRuntimeDirectory(runtimeRootDescriptor, taskHandle, record)
	if err != nil {
		return errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	if missing {
		return closeRuntimeRootDescriptor(runtimeRootDescriptor)
	}
	resultErr := removePinnedRuntimeAttachment(pinned, record)
	return errors.Join(resultErr, pinned.close())
}

func removeRuntimeAttachmentCreationIntent(
	runtimeRootDescriptor int,
	taskHandle string,
	_ runtimeAttachmentIdentityRecord,
) error {
	if !runtimeAttachmentPathAbsent(runtimeRootDescriptor, taskHandle) {
		return errors.Join(
			errRuntimeAttachmentPreparationUnproven,
			errors.New("task runtime directory identity is unproven; path preserved"),
		)
	}
	if !runtimeAttachmentPathAbsent(runtimeRootDescriptor, runtimeAttachmentCreationName(taskHandle)) {
		return errors.Join(
			errRuntimeAttachmentPreparationUnproven,
			errors.New("task runtime creation directory is unproven; path preserved"),
		)
	}
	return nil
}

func openRecordedTaskRuntimeDirectory(
	runtimeRootDescriptor int,
	taskHandle string,
	record runtimeAttachmentIdentityRecord,
) (*pinnedTaskRuntimeDirectory, bool, error) {
	names := []string{taskHandle}
	if record.Stage == runtimeAttachmentDirectoryBound || record.Stage == runtimeAttachmentCreating {
		names = append(names, runtimeAttachmentCreationName(taskHandle))
	} else if record.Stage == runtimeAttachmentReleaseIntent || record.Stage == runtimeAttachmentReleasing {
		names = append(names, runtimeAttachmentReleaseName(taskHandle))
	}
	var pinned *pinnedTaskRuntimeDirectory
	for _, name := range names {
		descriptor, identity, missing, err := openTaskRuntimeDirectory(runtimeRootDescriptor, name)
		if err != nil {
			return nil, false, err
		}
		if missing {
			continue
		}
		if pinned != nil {
			_ = unix.Close(descriptor)
			_ = unix.Close(pinned.taskDescriptor)
			return nil, false, errors.New("task runtime directory location is ambiguous; paths preserved")
		}
		generationMatches := runtimeAttachmentGenerationMatches(&pinnedTaskRuntimeDirectory{
			runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: descriptor,
			taskHandle: taskHandle, directoryName: name, taskIdentity: identity,
		}, record.Generation, record.GenerationID)
		directoryBound := record.Stage == runtimeAttachmentDirectoryBound &&
			(runtimeAttachmentDirectoryEmpty(descriptor) || generationMatches)
		canonicalSocketRequired := name == taskHandle &&
			(record.Stage == runtimeAttachmentActive || record.Stage == runtimeAttachmentReleaseIntent)
		isolatedSocketRequired := name == runtimeAttachmentReleaseName(taskHandle) &&
			record.Stage == runtimeAttachmentReleaseIntent
		if !runtimeAttachmentTransitionDirectoryMatches(identity, record.Task, generationMatches) ||
			!directoryBound && !generationMatches ||
			(canonicalSocketRequired || isolatedSocketRequired) &&
				!runtimeAttachmentSocketMatches(descriptor, record.Socket) {
			_ = unix.Close(descriptor)
			return nil, false, errors.New("task runtime directory identity differs; path preserved")
		}
		pinned = &pinnedTaskRuntimeDirectory{
			runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: descriptor,
			taskHandle: taskHandle, directoryName: name, taskIdentity: identity,
		}
	}
	if pinned != nil {
		return pinned, false, nil
	}
	for _, name := range names {
		err := reporter.QuarantineRuntimePath(
			runtimeRootDescriptor, name, record.Task, reporter.RuntimePathDirectory, 0o700,
		)
		if err == nil {
			return nil, true, nil
		}
		if !errors.Is(err, reporter.ErrRuntimePathMissing) {
			return nil, false, errors.New("task runtime directory quarantine is ambiguous; path preserved")
		}
	}
	return nil, true, nil
}

func removePinnedRuntimeAttachment(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) error {
	return removePinnedTaskRuntimeDirectory(pinned, record)
}

func removePinnedTaskRuntimeDirectory(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) error {
	if record.Stage == runtimeAttachmentDirectoryBound {
		generationMatches := runtimeAttachmentGenerationMatches(pinned, record.Generation, record.GenerationID)
		if !runtimeAttachmentDirectoryEmpty(pinned.taskDescriptor) && !generationMatches {
			return errors.New("task runtime creation directory is ambiguous; path preserved")
		}
		current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
		if err != nil || !runtimeAttachmentTransitionDirectoryMatches(current, record.Task, generationMatches) {
			return errors.New("task runtime creation directory identity differs; path preserved")
		}
		if err := reporter.QuarantineRuntimePath(
			pinned.runtimeRootDescriptor, pinned.directoryName, current, reporter.RuntimePathDirectory, 0o700,
		); err != nil {
			return errors.New("task runtime creation directory is unavailable")
		}
		return nil
	}
	if record.Stage == runtimeAttachmentCreating && !record.Socket.Valid() {
		if !runtimeAttachmentGenerationMatches(pinned, record.Generation, record.GenerationID) {
			return errors.New("task runtime creation directory is ambiguous; path preserved")
		}
		current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
		if err != nil {
			return err
		}
		if err := reporter.QuarantineRuntimePath(
			pinned.runtimeRootDescriptor, pinned.directoryName, current, reporter.RuntimePathDirectory, 0o700,
		); err != nil {
			return errors.New("task runtime creation directory is unavailable")
		}
		return nil
	}
	if record.Stage == runtimeAttachmentActive {
		staged := record
		staged.Stage = runtimeAttachmentReleaseIntent
		if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, staged, &record, nil); err != nil {
			return errors.New("task runtime attachment release cannot be staged")
		}
		record = staged
	}
	if record.Stage == runtimeAttachmentReleaseIntent || record.Stage == runtimeAttachmentReleasing {
		var err error
		record, err = isolatePinnedRuntimeAttachmentRelease(pinned, record)
		if err != nil {
			return err
		}
	}
	if err := reporter.QuarantineRuntimePath(
		pinned.taskDescriptor, "attachment.sock", record.Socket, reporter.RuntimePathSocket, 0o600,
	); err != nil && !errors.Is(err, reporter.ErrRuntimePathMissing) {
		return errors.New("task runtime attachment cannot be removed")
	}
	if !runtimeAttachmentPathAbsent(pinned.taskDescriptor, "attachment.sock") {
		return errors.New("task runtime attachment replacement was preserved")
	}
	current, err := stagePinnedRuntimeAttachmentDirectory(pinned, record)
	if err != nil {
		return err
	}
	if err := reporter.QuarantineRuntimePath(
		pinned.runtimeRootDescriptor, pinned.directoryName, current, reporter.RuntimePathDirectory, 0o700,
	); err != nil {
		return errors.New("task runtime directory is not empty or unavailable")
	}
	if !runtimeAttachmentPathAbsent(pinned.runtimeRootDescriptor, pinned.directoryName) {
		return errors.New("task runtime directory replacement was preserved")
	}
	return nil
}

func runtimeAttachmentSocketMatches(descriptor int, expected reporter.RuntimeSocketIdentity) bool {
	current, mode, found, err := readPinnedRuntimeSocketIdentity(descriptor)
	return err == nil && found && current == expected && mode&unix.S_IFMT == unix.S_IFSOCK && mode&0o777 == 0o600
}

func stagePinnedRuntimeAttachmentDirectory(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) (reporter.RuntimeSocketIdentity, error) {
	if err := unix.Fsync(pinned.taskDescriptor); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("task runtime directory update cannot be synchronized")
	}
	current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || !sameRuntimeAttachmentNode(current, pinned.taskIdentity) {
		return reporter.RuntimeSocketIdentity{}, errors.New("task runtime directory identity is unavailable")
	}
	staged := record
	staged.Task = current
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, staged, &record, nil); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("task runtime directory identity cannot be staged")
	}
	return current, nil
}
