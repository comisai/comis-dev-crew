package reporter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// ErrRuntimePathMissing reports that a removal target was already absent.
var ErrRuntimePathMissing = errors.New("runtime path is missing")

// ErrRuntimePathIdentity reports that a removal target did not have the authorized identity.
var ErrRuntimePathIdentity = errors.New("runtime path identity differs")

// RuntimePathKind identifies the filesystem object authorized for removal.
type RuntimePathKind uint8

const (
	// RuntimePathSocket authorizes an owner-scoped Unix socket.
	RuntimePathSocket RuntimePathKind = iota + 1
	// RuntimePathRegular authorizes an owner-scoped regular file.
	RuntimePathRegular
	// RuntimePathDirectory authorizes an owner-scoped directory.
	RuntimePathDirectory
)

// QuarantineRuntimePath atomically isolates, verifies, and removes one exact child identity.
func QuarantineRuntimePath(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) error {
	return quarantineRuntimePath(directoryDescriptor, name, expected, kind, permissions, nil)
}

func quarantineRuntimePath(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
	afterQuarantine func(RuntimeSocketIdentity) error,
) error {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(name) || !expected.Valid() ||
		!validRuntimePathKind(kind) || permissions.Perm() != permissions {
		return errors.New("runtime path removal authority is invalid")
	}
	quarantine := runtimePathQuarantineName(name, expected, kind, permissions)
	reconciled, err := reconcileQuarantinedRuntimePath(
		directoryDescriptor, quarantine, expected, kind, permissions,
	)
	if reconciled || err != nil {
		return err
	}
	targetDescriptor, err := pinExpectedRuntimePath(directoryDescriptor, name, expected, kind, permissions)
	if err != nil {
		return err
	}
	if err := renameRuntimePathNoReplace(directoryDescriptor, name, quarantine); err != nil {
		closeErr := closeRuntimeRemovalPin(targetDescriptor)
		if errors.Is(err, unix.ENOENT) {
			return errors.Join(ErrRuntimePathMissing, closeErr)
		}
		return errors.Join(errors.New("runtime path cannot be quarantined"), closeErr)
	}
	if err := unix.Fsync(directoryDescriptor); err != nil {
		return restorePinnedRuntimePath(
			directoryDescriptor, quarantine, name, targetDescriptor,
			errors.New("runtime path quarantine cannot be synchronized"),
		)
	}
	if afterQuarantine != nil {
		var pinnedStat unix.Stat_t
		pinnedErr := statRuntimeRemovalPin(targetDescriptor, &pinnedStat)
		pinnedIdentity, identityErr := runtimeSocketStatIdentity(pinnedStat)
		if pinnedErr != nil || identityErr != nil {
			return restorePinnedRuntimePath(
				directoryDescriptor, quarantine, name, targetDescriptor,
				errors.New("runtime path pinned identity is unavailable"),
			)
		}
		if err := afterQuarantine(pinnedIdentity); err != nil {
			return restorePinnedRuntimePath(directoryDescriptor, quarantine, name, targetDescriptor, err)
		}
	}
	return removePinnedRuntimePath(directoryDescriptor, quarantine, targetDescriptor, kind, permissions)
}

func reconcileQuarantinedRuntimePath(
	directoryDescriptor int,
	quarantine string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (bool, error) {
	descriptor, err := pinExpectedRuntimePath(directoryDescriptor, quarantine, expected, kind, permissions)
	if errors.Is(err, ErrRuntimePathMissing) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, removePinnedRuntimePath(directoryDescriptor, quarantine, descriptor, kind, permissions)
}

func pinExpectedRuntimePath(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (*runtimeRemovalPin, error) {
	descriptor, err := openRuntimeRemovalPath(directoryDescriptor, name, expected, kind, permissions)
	if errors.Is(err, unix.ENOENT) {
		return nil, ErrRuntimePathMissing
	}
	if errors.Is(err, ErrRuntimePathIdentity) {
		return nil, ErrRuntimePathIdentity
	}
	if err != nil {
		return nil, errors.New("runtime path identity is unavailable")
	}
	var stat unix.Stat_t
	if err := statRuntimeRemovalPin(descriptor, &stat); err != nil {
		_ = closeRuntimeRemovalPin(descriptor)
		return nil, errors.New("runtime path identity is unavailable")
	}
	identity, identityErr := runtimeSocketStatIdentity(stat)
	if identityErr != nil || !runtimeSocketIdentityMatches(identity, expected) ||
		!runtimePathModeMatches(uint32(stat.Mode), kind, permissions) ||
		(kind == RuntimePathRegular && stat.Nlink != 1) {
		_ = closeRuntimeRemovalPin(descriptor)
		return nil, ErrRuntimePathIdentity
	}
	return descriptor, nil
}

func removePinnedRuntimePath(
	directoryDescriptor int,
	name string,
	targetDescriptor *runtimeRemovalPin,
	kind RuntimePathKind,
	permissions os.FileMode,
) error {
	var pinnedStat unix.Stat_t
	var currentStat unix.Stat_t
	pinnedErr := statRuntimeRemovalPin(targetDescriptor, &pinnedStat)
	currentErr := unix.Fstatat(directoryDescriptor, name, &currentStat, unix.AT_SYMLINK_NOFOLLOW)
	if pinnedErr != nil || currentErr != nil || !runtimeStatsSameStableObject(pinnedStat, currentStat) ||
		!runtimePathModeMatches(uint32(currentStat.Mode), kind, permissions) {
		return errors.Join(ErrRuntimePathIdentity, closeRuntimeRemovalPin(targetDescriptor))
	}
	flags := 0
	if kind == RuntimePathDirectory {
		flags = unix.AT_REMOVEDIR
	}
	unlinkErr := unix.Unlinkat(directoryDescriptor, name, flags)
	syncErr := unix.Fsync(directoryDescriptor)
	closeErr := closeRuntimeRemovalPin(targetDescriptor)
	if unlinkErr != nil {
		return errors.Join(errors.New("runtime path quarantine cannot be removed"), syncErr, closeErr)
	}
	if syncErr != nil || closeErr != nil {
		return errors.New("runtime path quarantine removal cannot be synchronized")
	}
	return nil
}

func restorePinnedRuntimePath(
	directoryDescriptor int,
	quarantine, name string,
	targetDescriptor *runtimeRemovalPin,
	cause error,
) error {
	var pinnedStat unix.Stat_t
	var currentStat unix.Stat_t
	pinnedErr := statRuntimeRemovalPin(targetDescriptor, &pinnedStat)
	currentErr := unix.Fstatat(directoryDescriptor, quarantine, &currentStat, unix.AT_SYMLINK_NOFOLLOW)
	if pinnedErr != nil || currentErr != nil || !runtimeStatsSameStableObject(pinnedStat, currentStat) {
		return errors.Join(cause, ErrRuntimePathIdentity, closeRuntimeRemovalPin(targetDescriptor))
	}
	renameErr := renameRuntimePathNoReplace(directoryDescriptor, quarantine, name)
	syncErr := unix.Fsync(directoryDescriptor)
	closeErr := closeRuntimeRemovalPin(targetDescriptor)
	if renameErr != nil {
		return errors.Join(cause, errors.New("runtime path remains quarantined"), syncErr, closeErr)
	}
	return errors.Join(cause, syncErr, closeErr)
}

func runtimePathQuarantineName(
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) string {
	encoded := fmt.Sprintf(
		"%s\x00%016x:%016x:%016x:%016x:%016x:%016x:%d:%03o",
		name, expected.Device, expected.Inode, uint64(expected.ChangeSec), uint64(expected.ChangeNsec),
		uint64(expected.BirthSec), uint64(expected.BirthNsec), kind, permissions,
	)
	digest := sha256.Sum256([]byte(encoded))
	return ".devcrew-remove-" + hex.EncodeToString(digest[:])
}

// PublishRuntimePath atomically moves one exact prepared child into an absent destination name.
func PublishRuntimePath(
	directoryDescriptor int,
	temporaryName, destinationName string,
	expected RuntimeSocketIdentity,
	permissions os.FileMode,
) error {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(temporaryName) || !validRuntimeRemovalName(destinationName) ||
		temporaryName == destinationName || !expected.Valid() || permissions.Perm() != permissions {
		return errors.New("runtime path publication authority is invalid")
	}
	targetDescriptor, err := pinExpectedRuntimePath(
		directoryDescriptor, temporaryName, expected, RuntimePathRegular, permissions,
	)
	if err != nil {
		return errors.New("runtime path publication source differs")
	}
	if err := renameRuntimePathNoReplace(directoryDescriptor, temporaryName, destinationName); err != nil {
		return errors.Join(errors.New("runtime path cannot be published"), closeRuntimeRemovalPin(targetDescriptor))
	}
	if err := verifyPinnedRuntimePath(
		directoryDescriptor, destinationName, targetDescriptor, RuntimePathRegular, permissions,
	); err != nil {
		return errors.Join(errors.New("runtime path publication identity differs"), err, closeRuntimeRemovalPin(targetDescriptor))
	}
	syncErr := unix.Fsync(directoryDescriptor)
	closeErr := closeRuntimeRemovalPin(targetDescriptor)
	if syncErr != nil || closeErr != nil {
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
	if err := exchangeRuntimePaths(directoryDescriptor, temporaryName, destinationName); err != nil {
		return errors.Join(
			errors.New("runtime paths cannot be exchanged"),
			closeRuntimeRemovalPin(temporaryDescriptor), closeRuntimeRemovalPin(destinationDescriptor),
		)
	}
	temporaryErr := verifyPinnedRuntimePath(
		directoryDescriptor, destinationName, temporaryDescriptor, RuntimePathRegular, permissions,
	)
	destinationErr := verifyPinnedRuntimePath(
		directoryDescriptor, temporaryName, destinationDescriptor, RuntimePathRegular, permissions,
	)
	syncErr := unix.Fsync(directoryDescriptor)
	closeTemporaryErr := closeRuntimeRemovalPin(temporaryDescriptor)
	closeDestinationErr := closeRuntimeRemovalPin(destinationDescriptor)
	if temporaryErr != nil || destinationErr != nil {
		return errors.Join(
			errors.New("runtime path replacement identity differs"), temporaryErr, destinationErr,
			syncErr, closeTemporaryErr, closeDestinationErr,
		)
	}
	if syncErr != nil || closeTemporaryErr != nil || closeDestinationErr != nil {
		return errors.New("runtime path replacement cannot be synchronized")
	}
	return nil
}

func verifyPinnedRuntimePath(
	directoryDescriptor int,
	name string,
	targetDescriptor *runtimeRemovalPin,
	kind RuntimePathKind,
	permissions os.FileMode,
) error {
	var pinnedStat unix.Stat_t
	var currentStat unix.Stat_t
	if statRuntimeRemovalPin(targetDescriptor, &pinnedStat) != nil ||
		unix.Fstatat(directoryDescriptor, name, &currentStat, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!runtimeStatsSameStableObject(pinnedStat, currentStat) ||
		!runtimePathModeMatches(uint32(currentStat.Mode), kind, permissions) {
		return ErrRuntimePathIdentity
	}
	return nil
}

func runtimeStatsSameStableObject(left, right unix.Stat_t) bool {
	leftIdentity, leftErr := runtimeSocketStatIdentity(left)
	rightIdentity, rightErr := runtimeSocketStatIdentity(right)
	return leftErr == nil && rightErr == nil && runtimeSocketIdentityMatches(leftIdentity, rightIdentity)
}

func runtimeSocketIdentityMatches(current, expected RuntimeSocketIdentity) bool {
	if current.Device != expected.Device || current.Inode != expected.Inode {
		return false
	}
	if expected.BirthSec != 0 || expected.BirthNsec != 0 {
		return current.BirthSec == expected.BirthSec && current.BirthNsec == expected.BirthNsec
	}
	return current.ChangeSec == expected.ChangeSec && current.ChangeNsec == expected.ChangeNsec
}

func validRuntimeRemovalName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		len([]byte(name)) <= 128 && !strings.ContainsAny(name, "\x00\r\n")
}

func validRuntimePathKind(kind RuntimePathKind) bool {
	return kind == RuntimePathSocket || kind == RuntimePathRegular || kind == RuntimePathDirectory
}

func runtimePathModeMatches(mode uint32, kind RuntimePathKind, permissions os.FileMode) bool {
	wantType := uint32(unix.S_IFSOCK)
	if kind == RuntimePathRegular {
		wantType = unix.S_IFREG
	} else if kind == RuntimePathDirectory {
		wantType = unix.S_IFDIR
	}
	return mode&unix.S_IFMT == wantType && os.FileMode(mode&0o777) == permissions
}
