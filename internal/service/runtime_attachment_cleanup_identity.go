package service

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

var errRuntimeAttachmentOwnershipUnproven = errors.New("runtime attachment filesystem ownership is unproven")

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
		canonicalAbsent, canonicalErr := inspectRuntimeAttachmentPathAbsent(runtimeRootDescriptor, taskHandle)
		creationAbsent, creationErr := inspectRuntimeAttachmentPathAbsent(
			runtimeRootDescriptor, runtimeAttachmentCreationName(taskHandle),
		)
		if canonicalErr != nil || creationErr != nil {
			return errors.Join(canonicalErr, creationErr, closeRuntimeRootDescriptor(runtimeRootDescriptor))
		}
		if !canonicalAbsent || !creationAbsent {
			return errors.Join(
				errRuntimeAttachmentOwnershipUnproven,
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
	canonicalAbsent, canonicalErr := inspectRuntimeAttachmentPathAbsent(runtimeRootDescriptor, taskHandle)
	if canonicalErr != nil {
		return canonicalErr
	}
	if !canonicalAbsent {
		return errors.Join(
			errRuntimeAttachmentOwnershipUnproven,
			errors.New("task runtime directory identity is unproven; path preserved"),
		)
	}
	creationAbsent, creationErr := inspectRuntimeAttachmentPathAbsent(
		runtimeRootDescriptor, runtimeAttachmentCreationName(taskHandle),
	)
	if creationErr != nil {
		return creationErr
	}
	if !creationAbsent {
		return errors.Join(
			errRuntimeAttachmentOwnershipUnproven,
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
			return nil, false, errors.Join(
				errRuntimeAttachmentOwnershipUnproven,
				errors.New("task runtime directory location is ambiguous; paths preserved"),
			)
		}
		generationMatches, generationErr := inspectRuntimeAttachmentGeneration(&pinnedTaskRuntimeDirectory{
			runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: descriptor,
			taskHandle: taskHandle, directoryName: name, taskIdentity: identity,
		}, record.Generation, record.GenerationID)
		if generationErr != nil && !errors.Is(generationErr, errRuntimeAttachmentGenerationDiffers) {
			_ = unix.Close(descriptor)
			return nil, false, generationErr
		}
		directoryEmpty, emptyErr := runtimeAttachmentDirectoryEmpty(descriptor)
		if emptyErr != nil {
			_ = unix.Close(descriptor)
			return nil, false, emptyErr
		}
		directoryBound := record.Stage == runtimeAttachmentDirectoryBound && (directoryEmpty || generationMatches)
		canonicalSocketRequired := name == taskHandle &&
			(record.Stage == runtimeAttachmentActive || record.Stage == runtimeAttachmentReleaseIntent)
		isolatedSocketRequired := name == runtimeAttachmentReleaseName(taskHandle) &&
			record.Stage == runtimeAttachmentReleaseIntent
		socketMatches, socketErr := inspectRuntimeAttachmentSocket(descriptor, record.Socket)
		if socketErr != nil {
			_ = unix.Close(descriptor)
			return nil, false, socketErr
		}
		if !runtimeAttachmentTransitionDirectoryMatches(identity, record.Task) ||
			!directoryBound && !generationMatches ||
			(canonicalSocketRequired || isolatedSocketRequired) &&
				!socketMatches {
			_ = unix.Close(descriptor)
			return nil, false, errors.Join(
				errRuntimeAttachmentOwnershipUnproven,
				errors.New("task runtime directory identity differs; path preserved"),
			)
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
		if errors.Is(err, reporter.ErrRuntimePathIdentity) {
			return nil, false, errors.Join(
				errRuntimeAttachmentOwnershipUnproven,
				errors.New("task runtime directory quarantine is ambiguous; path preserved"),
				err,
			)
		}
		if !errors.Is(err, reporter.ErrRuntimePathMissing) {
			return nil, false, errors.Join(errors.New("task runtime directory quarantine is unavailable"), err)
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
		directoryEmpty, err := runtimeAttachmentDirectoryEmpty(pinned.taskDescriptor)
		if err != nil {
			return err
		}
		generationMatches, err := inspectRuntimeAttachmentGeneration(pinned, record.Generation, record.GenerationID)
		if err != nil && !errors.Is(err, errRuntimeAttachmentGenerationDiffers) {
			return err
		}
		if !directoryEmpty && !generationMatches {
			return runtimeAttachmentOwnershipUnproven("task runtime creation directory is ambiguous; path preserved")
		}
		current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
		if err != nil {
			return err
		}
		if !runtimeAttachmentTransitionDirectoryMatches(current, record.Task) {
			return runtimeAttachmentOwnershipUnproven("task runtime creation directory identity differs; path preserved")
		}
		if err := reporter.QuarantineRuntimePath(
			pinned.runtimeRootDescriptor, pinned.directoryName, current, reporter.RuntimePathDirectory, 0o700,
		); err != nil {
			return classifyRuntimeAttachmentCleanupPathError("task runtime creation directory is unavailable", err)
		}
		return nil
	}
	if record.Stage == runtimeAttachmentCreating && !record.Socket.Valid() {
		generationMatches, err := inspectRuntimeAttachmentGeneration(pinned, record.Generation, record.GenerationID)
		if err != nil && !errors.Is(err, errRuntimeAttachmentGenerationDiffers) {
			return err
		}
		if !generationMatches {
			return runtimeAttachmentOwnershipUnproven("task runtime creation directory is ambiguous; path preserved")
		}
		current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
		if err != nil {
			return err
		}
		if err := reporter.QuarantineRuntimePath(
			pinned.runtimeRootDescriptor, pinned.directoryName, current, reporter.RuntimePathDirectory, 0o700,
		); err != nil {
			return classifyRuntimeAttachmentCleanupPathError("task runtime creation directory is unavailable", err)
		}
		return nil
	}
	if record.Stage == runtimeAttachmentActive {
		staged := record
		staged.Stage = runtimeAttachmentReleaseIntent
		if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, staged, &record, nil); err != nil {
			return classifyRuntimeAttachmentCleanupPathError("task runtime attachment release cannot be staged", err)
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
		return classifyRuntimeAttachmentCleanupPathError("task runtime attachment cannot be removed", err)
	}
	socketAbsent, err := inspectRuntimeAttachmentPathAbsent(pinned.taskDescriptor, "attachment.sock")
	if err != nil {
		return err
	}
	if !socketAbsent {
		return runtimeAttachmentOwnershipUnproven("task runtime attachment replacement was preserved")
	}
	current, err := stagePinnedRuntimeAttachmentDirectory(pinned, record)
	if err != nil {
		return err
	}
	if err := reporter.QuarantineRuntimePath(
		pinned.runtimeRootDescriptor, pinned.directoryName, current, reporter.RuntimePathDirectory, 0o700,
	); err != nil {
		return classifyRuntimeAttachmentCleanupPathError("task runtime directory is not empty or unavailable", err)
	}
	directoryAbsent, err := inspectRuntimeAttachmentPathAbsent(pinned.runtimeRootDescriptor, pinned.directoryName)
	if err != nil {
		return err
	}
	if !directoryAbsent {
		return runtimeAttachmentOwnershipUnproven("task runtime directory replacement was preserved")
	}
	return nil
}

func inspectRuntimeAttachmentSocket(descriptor int, expected reporter.RuntimeSocketIdentity) (bool, error) {
	current, mode, found, err := readPinnedRuntimeSocketIdentity(descriptor)
	if err != nil {
		return false, err
	}
	return found && current == expected && mode&unix.S_IFMT == unix.S_IFSOCK && mode&0o777 == 0o600, nil
}

func stagePinnedRuntimeAttachmentDirectory(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) (reporter.RuntimeSocketIdentity, error) {
	if err := unix.Fsync(pinned.taskDescriptor); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("task runtime directory update cannot be synchronized")
	}
	current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	if !sameRuntimeAttachmentNode(current, pinned.taskIdentity) {
		return reporter.RuntimeSocketIdentity{}, runtimeAttachmentOwnershipUnproven("task runtime directory identity differs")
	}
	staged := record
	staged.Task = current
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, staged, &record, nil); err != nil {
		return reporter.RuntimeSocketIdentity{}, classifyRuntimeAttachmentCleanupPathError(
			"task runtime directory identity cannot be staged", err,
		)
	}
	return current, nil
}

func runtimeAttachmentOwnershipUnproven(message string) error {
	return errors.Join(errRuntimeAttachmentOwnershipUnproven, errors.New(message))
}

func classifyRuntimeAttachmentCleanupPathError(message string, err error) error {
	if errors.Is(err, reporter.ErrRuntimePathIdentity) || errors.Is(err, reporter.ErrRuntimePathMissing) {
		return errors.Join(errRuntimeAttachmentOwnershipUnproven, errors.New(message), err)
	}
	return errors.Join(errors.New(message), err)
}
