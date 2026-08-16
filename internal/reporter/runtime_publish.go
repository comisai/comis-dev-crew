package reporter

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// PublishRuntimePath atomically moves one exact prepared child into an absent destination name.
func PublishRuntimePath(
	directoryDescriptor int,
	temporaryName, destinationName string,
	expected RuntimeSocketIdentity,
	permissions os.FileMode,
) error {
	return publishRuntimePath(directoryDescriptor, temporaryName, destinationName, expected, permissions, nil)
}

// PublishRuntimeDirectory atomically moves one exact prepared directory into an absent destination name.
func PublishRuntimeDirectory(
	directoryDescriptor int,
	temporaryName, destinationName string,
	expected RuntimeSocketIdentity,
	permissions os.FileMode,
) error {
	return publishRuntimePathKind(
		directoryDescriptor, temporaryName, destinationName, expected, RuntimePathDirectory, permissions, nil,
	)
}

func publishRuntimePath(
	directoryDescriptor int,
	temporaryName, destinationName string,
	expected RuntimeSocketIdentity,
	permissions os.FileMode,
	afterPin func() error,
) error {
	return publishRuntimePathKind(
		directoryDescriptor, temporaryName, destinationName, expected, RuntimePathRegular, permissions, afterPin,
	)
}

func publishRuntimePathKind(
	directoryDescriptor int,
	temporaryName, destinationName string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
	afterPin func() error,
) error {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(temporaryName) || !validRuntimeRemovalName(destinationName) ||
		temporaryName == destinationName || !expected.Valid() || !validRuntimePathKind(kind) || permissions.Perm() != permissions {
		return errors.New("runtime path publication authority is invalid")
	}
	targetDescriptor, err := pinExpectedRuntimePath(
		directoryDescriptor, temporaryName, expected, kind, permissions,
	)
	if err != nil {
		return errors.New("runtime path publication source differs")
	}
	if afterPin != nil {
		if err := afterPin(); err != nil {
			return errors.Join(err, closeRuntimeRemovalPin(targetDescriptor))
		}
	}
	if err := renameRuntimePathNoReplace(directoryDescriptor, temporaryName, destinationName); err != nil {
		return errors.Join(errors.New("runtime path cannot be published"), closeRuntimeRemovalPin(targetDescriptor))
	}
	if err := verifyPinnedRuntimePath(
		directoryDescriptor, destinationName, targetDescriptor, kind, permissions,
	); err != nil {
		return restoreMovedRuntimePath(
			directoryDescriptor, destinationName, temporaryName, temporaryName, kind, permissions,
			errors.Join(errors.New("runtime path publication identity differs"), err, closeRuntimeRemovalPin(targetDescriptor)),
		)
	}
	syncErr := unix.Fsync(directoryDescriptor)
	if syncErr != nil {
		return restoreMovedRuntimePath(
			directoryDescriptor, destinationName, temporaryName, temporaryName, kind, permissions,
			errors.Join(errors.New("runtime path publication cannot be synchronized"),
				syncErr, closeRuntimeRemovalPin(targetDescriptor)),
		)
	}
	closeErr := closeRuntimeRemovalPin(targetDescriptor)
	if closeErr != nil {
		return errors.New("runtime path publication cannot be synchronized")
	}
	return nil
}

// ReplaceRuntimePath atomically exchanges two exact owner-only regular files.
func ReplaceRuntimePath(
	directoryDescriptor int,
	temporaryName, destinationName string,
	temporaryIdentity, destinationIdentity RuntimeSocketIdentity,
	permissions os.FileMode,
) error {
	return replaceRuntimePath(
		directoryDescriptor, temporaryName, destinationName,
		temporaryIdentity, destinationIdentity, permissions, nil,
	)
}

func replaceRuntimePath(
	directoryDescriptor int,
	temporaryName, destinationName string,
	temporaryIdentity, destinationIdentity RuntimeSocketIdentity,
	permissions os.FileMode,
	afterPins func() error,
) error {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(temporaryName) || !validRuntimeRemovalName(destinationName) ||
		temporaryName == destinationName || !temporaryIdentity.Valid() || !destinationIdentity.Valid() ||
		permissions.Perm() != permissions {
		return errors.New("runtime path replacement authority is invalid")
	}
	temporaryDescriptor, err := pinExpectedRuntimePath(
		directoryDescriptor, temporaryName, temporaryIdentity, RuntimePathRegular, permissions,
	)
	if err != nil {
		return errors.New("runtime path replacement source differs")
	}
	destinationDescriptor, err := pinExpectedRuntimePath(
		directoryDescriptor, destinationName, destinationIdentity, RuntimePathRegular, permissions,
	)
	if err != nil {
		return errors.Join(errors.New("runtime path replacement destination differs"), closeRuntimeRemovalPin(temporaryDescriptor))
	}
	if afterPins != nil {
		if err := afterPins(); err != nil {
			return errors.Join(err, closeRuntimeRemovalPin(temporaryDescriptor), closeRuntimeRemovalPin(destinationDescriptor))
		}
	}
	if err := exchangeRuntimePaths(directoryDescriptor, temporaryName, destinationName); err != nil {
		return errors.Join(errors.New("runtime paths cannot be exchanged"),
			closeRuntimeRemovalPin(temporaryDescriptor), closeRuntimeRemovalPin(destinationDescriptor))
	}
	temporaryErr := verifyPinnedRuntimePath(
		directoryDescriptor, destinationName, temporaryDescriptor, RuntimePathRegular, permissions,
	)
	destinationErr := verifyPinnedRuntimePath(
		directoryDescriptor, temporaryName, destinationDescriptor, RuntimePathRegular, permissions,
	)
	if temporaryErr != nil || destinationErr != nil {
		cause := errors.Join(errors.New("runtime path replacement identity differs"), temporaryErr, destinationErr,
			closeRuntimeRemovalPin(temporaryDescriptor), closeRuntimeRemovalPin(destinationDescriptor))
		return rollbackRuntimePathExchange(directoryDescriptor, temporaryName, destinationName, permissions, cause)
	}
	syncErr := unix.Fsync(directoryDescriptor)
	closeTemporaryErr := closeRuntimeRemovalPin(temporaryDescriptor)
	closeDestinationErr := closeRuntimeRemovalPin(destinationDescriptor)
	if syncErr != nil {
		return rollbackRuntimePathExchange(
			directoryDescriptor, temporaryName, destinationName, permissions,
			errors.Join(errors.New("runtime path replacement cannot be synchronized"),
				syncErr, closeTemporaryErr, closeDestinationErr),
		)
	}
	if closeTemporaryErr != nil || closeDestinationErr != nil {
		return errors.New("runtime path replacement cannot be synchronized")
	}
	return nil
}

func rollbackRuntimePathExchange(
	directoryDescriptor int,
	temporaryName, destinationName string,
	permissions os.FileMode,
	cause error,
) error {
	temporary, temporaryErr := pinCurrentRuntimePath(
		directoryDescriptor, temporaryName, temporaryName, RuntimePathRegular, permissions,
	)
	destination, destinationErr := pinCurrentRuntimePath(
		directoryDescriptor, destinationName, destinationName, RuntimePathRegular, permissions,
	)
	if temporaryErr != nil || destinationErr != nil {
		if temporary != nil {
			_ = closeRuntimeRemovalPin(temporary)
		}
		if destination != nil {
			_ = closeRuntimeRemovalPin(destination)
		}
		return errors.Join(cause, ErrRuntimePathIdentity)
	}
	if err := exchangeRuntimePaths(directoryDescriptor, temporaryName, destinationName); err != nil {
		return errors.Join(cause, errors.New("runtime path replacement cannot be restored"),
			closeRuntimeRemovalPin(temporary), closeRuntimeRemovalPin(destination))
	}
	temporaryVerify := verifyPinnedRuntimePath(
		directoryDescriptor, destinationName, temporary, RuntimePathRegular, permissions,
	)
	destinationVerify := verifyPinnedRuntimePath(
		directoryDescriptor, temporaryName, destination, RuntimePathRegular, permissions,
	)
	return errors.Join(cause, temporaryVerify, destinationVerify, unix.Fsync(directoryDescriptor),
		closeRuntimeRemovalPin(temporary), closeRuntimeRemovalPin(destination))
}
