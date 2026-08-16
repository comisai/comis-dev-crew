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
	_, err := PublishRuntimeDirectoryIdentity(
		directoryDescriptor, temporaryName, destinationName, expected, permissions,
	)
	return err
}

// PublishRuntimeDirectoryIdentity moves one exact directory and returns its pinned post-rename identity.
func PublishRuntimeDirectoryIdentity(
	directoryDescriptor int,
	temporaryName, destinationName string,
	expected RuntimeSocketIdentity,
	permissions os.FileMode,
) (RuntimeSocketIdentity, error) {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(temporaryName) || !validRuntimeRemovalName(destinationName) ||
		temporaryName == destinationName || !expected.Valid() || permissions.Perm() != permissions {
		return RuntimeSocketIdentity{}, errors.New("runtime directory publication authority is invalid")
	}
	targetDescriptor, err := pinExpectedRuntimePath(
		directoryDescriptor, temporaryName, expected, RuntimePathDirectory, permissions,
	)
	if err != nil {
		return RuntimeSocketIdentity{}, errors.New("runtime directory publication source differs")
	}
	if err := renameRuntimePathNoReplace(directoryDescriptor, temporaryName, destinationName); err != nil {
		return RuntimeSocketIdentity{}, errors.Join(
			errors.New("runtime directory cannot be published"), closeRuntimeRemovalPin(targetDescriptor),
		)
	}
	identity, err := verifyPublishedRuntimeDirectory(
		directoryDescriptor, destinationName, targetDescriptor, expected, permissions,
	)
	if err != nil {
		return RuntimeSocketIdentity{}, preserveMovedRuntimePathFailure(
			directoryDescriptor, targetDescriptor, RuntimePathDirectory,
			errors.Join(errors.New("runtime directory publication identity differs"), err),
		)
	}
	if err := unix.Fsync(directoryDescriptor); err != nil {
		return RuntimeSocketIdentity{}, preserveMovedRuntimePathFailure(
			directoryDescriptor, targetDescriptor, RuntimePathDirectory,
			errors.Join(errors.New("runtime directory publication cannot be synchronized"), err),
		)
	}
	if err := closeRuntimeRemovalPin(targetDescriptor); err != nil {
		return RuntimeSocketIdentity{}, errors.New("runtime directory publication cannot be synchronized")
	}
	return identity, nil
}

func verifyPublishedRuntimeDirectory(
	directoryDescriptor int,
	name string,
	targetDescriptor *runtimeRemovalPin,
	expected RuntimeSocketIdentity,
	permissions os.FileMode,
) (RuntimeSocketIdentity, error) {
	var pinnedStat unix.Stat_t
	var currentStat unix.Stat_t
	if statRuntimeRemovalPin(targetDescriptor, &pinnedStat) != nil ||
		unix.Fstatat(directoryDescriptor, name, &currentStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!runtimeStatsSameStableObject(pinnedStat, currentStat) ||
		!runtimePathModeMatches(uint32(currentStat.Mode), RuntimePathDirectory, permissions) {
		return RuntimeSocketIdentity{}, ErrRuntimePathIdentity
	}
	identity, err := runtimeRemovalPinIdentity(targetDescriptor, pinnedStat)
	if err != nil || identity.Device != expected.Device || identity.Inode != expected.Inode ||
		(expected.BirthSec != 0 || expected.BirthNsec != 0) &&
			(identity.BirthSec != expected.BirthSec || identity.BirthNsec != expected.BirthNsec) {
		return RuntimeSocketIdentity{}, ErrRuntimePathIdentity
	}
	return identity, nil
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
		return preserveMovedRuntimePathFailure(
			directoryDescriptor, targetDescriptor, kind,
			errors.Join(errors.New("runtime path publication identity differs"), err),
		)
	}
	syncErr := unix.Fsync(directoryDescriptor)
	if syncErr != nil {
		return preserveMovedRuntimePathFailure(
			directoryDescriptor, targetDescriptor, kind,
			errors.Join(errors.New("runtime path publication cannot be synchronized"), syncErr),
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
		cause := errors.Join(errors.New("runtime path replacement identity differs"), temporaryErr, destinationErr)
		return preserveRuntimePathExchangeFailure(
			directoryDescriptor, temporaryDescriptor, destinationDescriptor, cause,
		)
	}
	syncErr := unix.Fsync(directoryDescriptor)
	if syncErr != nil {
		return preserveRuntimePathExchangeFailure(
			directoryDescriptor, temporaryDescriptor, destinationDescriptor,
			errors.Join(errors.New("runtime path replacement cannot be synchronized"), syncErr),
		)
	}
	closeTemporaryErr := closeRuntimeRemovalPin(temporaryDescriptor)
	closeDestinationErr := closeRuntimeRemovalPin(destinationDescriptor)
	if closeTemporaryErr != nil || closeDestinationErr != nil {
		return errors.New("runtime path replacement cannot be synchronized")
	}
	return nil
}

func preserveRuntimePathExchangeFailure(
	directoryDescriptor int,
	temporaryDescriptor, destinationDescriptor *runtimeRemovalPin,
	cause error,
) error {
	return errors.Join(cause, ErrRuntimePathIdentity, errors.New("runtime path replacement remains exchanged"),
		unix.Fsync(directoryDescriptor), preserveRuntimeRemovalPin(temporaryDescriptor, RuntimePathRegular),
		preserveRuntimeRemovalPin(destinationDescriptor, RuntimePathRegular))
}
