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

type runtimePathMutationHooks struct {
	afterPin     func() error
	beforeUnlink func(int, string) error
}

const runtimePathIsolationTarget = "target"

func quarantineRuntimePath(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
	afterQuarantine func(RuntimeSocketIdentity) error,
) error {
	return quarantineRuntimePathWithHooks(
		directoryDescriptor, name, expected, kind, permissions, runtimePathMutationHooks{}, afterQuarantine,
	)
}

func quarantineRuntimePathWithHooks(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
	hooks runtimePathMutationHooks,
	afterQuarantine func(RuntimeSocketIdentity) error,
) error {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(name) || !expected.Valid() ||
		!validRuntimePathKind(kind) || permissions.Perm() != permissions {
		return errors.New("runtime path removal authority is invalid")
	}
	if !exclusiveRuntimeRemovalDirectory(directoryDescriptor) {
		return errors.New("runtime path removal namespace is unsafe")
	}
	isolationName := runtimePathQuarantineName(name, expected, kind, permissions)
	isolationDescriptor, created, err := openRuntimePathIsolation(directoryDescriptor, isolationName)
	if err != nil {
		return err
	}
	if !created {
		reconciled, reconcileErr := reconcileIsolatedRuntimePath(
			directoryDescriptor, isolationDescriptor, isolationName, name, expected, kind, permissions,
		)
		if reconciled || reconcileErr != nil {
			return reconcileErr
		}
		isolationDescriptor, _, err = openRuntimePathIsolation(directoryDescriptor, isolationName)
		if err != nil {
			return err
		}
	}
	targetDescriptor, err := pinExpectedRuntimePathWithAnchor(
		directoryDescriptor, name, isolationDescriptor, name, expected, kind, permissions,
	)
	if err != nil {
		return errors.Join(err, discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor, isolationName))
	}
	if hooks.afterPin != nil {
		if err := hooks.afterPin(); err != nil {
			return errors.Join(err, closeRuntimeRemovalPin(targetDescriptor),
				discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor, isolationName))
		}
	}
	if err := renameRuntimePathNoReplaceBetween(
		directoryDescriptor, name, isolationDescriptor, runtimePathIsolationTarget,
	); err != nil {
		closeErr := closeRuntimeRemovalPin(targetDescriptor)
		discardErr := discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor, isolationName)
		if errors.Is(err, unix.ENOENT) {
			return errors.Join(ErrRuntimePathMissing, closeErr, discardErr)
		}
		return errors.Join(errors.New("runtime path cannot be quarantined"), closeErr, discardErr)
	}
	if err := errors.Join(unix.Fsync(directoryDescriptor), unix.Fsync(isolationDescriptor)); err != nil {
		return restoreIsolatedRuntimePath(
			directoryDescriptor, isolationDescriptor, isolationName, name, targetDescriptor,
			errors.New("runtime path quarantine cannot be synchronized"),
		)
	}
	if afterQuarantine != nil {
		var pinnedStat unix.Stat_t
		pinnedErr := statRuntimeRemovalPin(targetDescriptor, &pinnedStat)
		pinnedIdentity, identityErr := runtimeRemovalPinIdentity(targetDescriptor, pinnedStat)
		if pinnedErr != nil || identityErr != nil {
			return restoreIsolatedRuntimePath(
				directoryDescriptor, isolationDescriptor, isolationName, name, targetDescriptor,
				errors.New("runtime path pinned identity is unavailable"),
			)
		}
		if err := afterQuarantine(pinnedIdentity); err != nil {
			return restoreIsolatedRuntimePath(
				directoryDescriptor, isolationDescriptor, isolationName, name, targetDescriptor, err,
			)
		}
	}
	if err := verifyPinnedRuntimePath(
		isolationDescriptor, runtimePathIsolationTarget, targetDescriptor, kind, permissions,
	); err != nil {
		return restoreIsolatedRuntimePath(
			directoryDescriptor, isolationDescriptor, isolationName, name, targetDescriptor,
			errors.Join(ErrRuntimePathIdentity, err),
		)
	}
	if err := removePinnedRuntimePath(
		isolationDescriptor, runtimePathIsolationTarget, targetDescriptor, kind, permissions, hooks.beforeUnlink,
	); err != nil {
		return errors.Join(err, unix.Close(isolationDescriptor))
	}
	return discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor, isolationName)
}

func exclusiveRuntimeRemovalDirectory(directoryDescriptor int) bool {
	var stat unix.Stat_t
	return unix.Fstat(directoryDescriptor, &stat) == nil && stat.Mode&unix.S_IFMT == unix.S_IFDIR &&
		stat.Mode&0o777 == 0o700 && stat.Uid == uint32(unix.Geteuid())
}

func reconcileIsolatedRuntimePath(
	directoryDescriptor int,
	isolationDescriptor int,
	isolationName string,
	originalName string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (bool, error) {
	descriptor, err := pinExpectedRuntimePathWithAnchor(
		isolationDescriptor, runtimePathIsolationTarget, isolationDescriptor, originalName,
		expected, kind, permissions,
	)
	if errors.Is(err, ErrRuntimePathMissing) {
		if anchorErr := reconcileRuntimeRemovalAnchor(
			isolationDescriptor, originalName, expected, kind, permissions,
		); anchorErr != nil {
			return true, errors.Join(ErrRuntimePathIdentity, anchorErr, unix.Close(isolationDescriptor))
		}
		if discardErr := discardRuntimePathIsolation(
			directoryDescriptor, isolationDescriptor, isolationName,
		); discardErr != nil {
			return true, errors.Join(ErrRuntimePathIdentity, discardErr)
		}
		return true, nil
	}
	if err != nil {
		return true, errors.Join(err, unix.Close(isolationDescriptor))
	}
	if err := removePinnedRuntimePath(
		isolationDescriptor, runtimePathIsolationTarget, descriptor, kind, permissions, nil,
	); err != nil {
		return true, errors.Join(err, unix.Close(isolationDescriptor))
	}
	return true, discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor, isolationName)
}

func openRuntimePathIsolation(directoryDescriptor int, name string) (int, bool, error) {
	created := true
	if err := unix.Mkdirat(directoryDescriptor, name, 0o700); err != nil {
		if !errors.Is(err, unix.EEXIST) {
			return -1, false, errors.New("runtime path isolation is unavailable")
		}
		created = false
	}
	descriptor, err := unix.Openat(
		directoryDescriptor, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		return -1, false, errors.New("runtime path isolation is unavailable")
	}
	var stat unix.Stat_t
	if unix.Fstat(descriptor, &stat) != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 ||
		stat.Uid != uint32(unix.Geteuid()) {
		_ = unix.Close(descriptor)
		return -1, false, ErrRuntimePathIdentity
	}
	if created && unix.Fsync(directoryDescriptor) != nil {
		return -1, false, errors.Join(
			errors.New("runtime path isolation cannot be synchronized"), unix.Close(descriptor),
		)
	}
	return descriptor, created, nil
}

func discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor int, isolationName string) error {
	closeErr := unix.Close(isolationDescriptor)
	removeErr := unix.Unlinkat(directoryDescriptor, isolationName, unix.AT_REMOVEDIR)
	syncErr := unix.Fsync(directoryDescriptor)
	if removeErr != nil {
		return errors.Join(errors.New("runtime path isolation remains"), closeErr, syncErr)
	}
	return errors.Join(closeErr, syncErr)
}

func restoreIsolatedRuntimePath(
	directoryDescriptor int,
	isolationDescriptor int,
	isolationName string,
	name string,
	targetDescriptor *runtimeRemovalPin,
	cause error,
) error {
	renameErr := renameRuntimePathNoReplaceBetween(
		isolationDescriptor, runtimePathIsolationTarget, directoryDescriptor, name,
	)
	syncErr := errors.Join(unix.Fsync(isolationDescriptor), unix.Fsync(directoryDescriptor))
	closeErr := closeRuntimeRemovalPin(targetDescriptor)
	if renameErr != nil {
		return errors.Join(cause, errors.New("runtime path remains isolated"), syncErr, closeErr,
			unix.Close(isolationDescriptor))
	}
	return errors.Join(cause, syncErr, closeErr,
		discardRuntimePathIsolation(directoryDescriptor, isolationDescriptor, isolationName))
}

func pinExpectedRuntimePath(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (*runtimeRemovalPin, error) {
	return pinExpectedRuntimePathAt(directoryDescriptor, name, name, expected, kind, permissions)
}

func pinExpectedRuntimePathAt(
	directoryDescriptor int,
	name, anchorName string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (*runtimeRemovalPin, error) {
	return pinExpectedRuntimePathWithAnchor(
		directoryDescriptor, name, directoryDescriptor, anchorName, expected, kind, permissions,
	)
}

func pinExpectedRuntimePathWithAnchor(
	directoryDescriptor int,
	name string,
	anchorDirectoryDescriptor int,
	anchorName string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (*runtimeRemovalPin, error) {
	descriptor, err := openRuntimeRemovalPath(
		directoryDescriptor, name, anchorName, anchorDirectoryDescriptor, expected, kind, permissions,
	)
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
	identity, identityErr := runtimeRemovalPinIdentity(descriptor, stat)
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
	beforeUnlink func(int, string) error,
) error {
	var pinnedStat unix.Stat_t
	var currentStat unix.Stat_t
	pinnedErr := statRuntimeRemovalPin(targetDescriptor, &pinnedStat)
	currentErr := unix.Fstatat(directoryDescriptor, name, &currentStat, unix.AT_SYMLINK_NOFOLLOW)
	if pinnedErr != nil || currentErr != nil || !runtimeStatsSameStableObject(pinnedStat, currentStat) ||
		!runtimePathModeMatches(uint32(currentStat.Mode), kind, permissions) {
		return errors.Join(ErrRuntimePathIdentity, closeRuntimeRemovalPin(targetDescriptor))
	}
	if beforeUnlink != nil {
		if err := beforeUnlink(directoryDescriptor, name); err != nil {
			return errors.Join(err, closeRuntimeRemovalPin(targetDescriptor))
		}
		currentErr = unix.Fstatat(directoryDescriptor, name, &currentStat, unix.AT_SYMLINK_NOFOLLOW)
		if currentErr != nil || !runtimeStatsSameStableObject(pinnedStat, currentStat) ||
			!runtimePathModeMatches(uint32(currentStat.Mode), kind, permissions) {
			return errors.Join(ErrRuntimePathIdentity, closeRuntimeRemovalPin(targetDescriptor))
		}
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

func restoreMovedRuntimePath(
	directoryDescriptor int,
	from, to, anchorName string,
	kind RuntimePathKind,
	permissions os.FileMode,
	cause error,
) error {
	current, err := pinCurrentRuntimePath(directoryDescriptor, from, anchorName, kind, permissions)
	if err != nil {
		return errors.Join(cause, ErrRuntimePathIdentity)
	}
	if err := renameRuntimePathNoReplace(directoryDescriptor, from, to); err != nil {
		return errors.Join(cause, errors.New("runtime path remains moved"), closeRuntimeRemovalPin(current))
	}
	verifyErr := verifyPinnedRuntimePath(directoryDescriptor, to, current, kind, permissions)
	syncErr := unix.Fsync(directoryDescriptor)
	closeErr := closeRuntimeRemovalPin(current)
	return errors.Join(cause, verifyErr, syncErr, closeErr)
}

func pinCurrentRuntimePath(
	directoryDescriptor int,
	name, anchorName string,
	kind RuntimePathKind,
	permissions os.FileMode,
) (*runtimeRemovalPin, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, name, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	identity, err := runtimeSocketStatIdentity(stat)
	if err != nil || !runtimePathModeMatches(uint32(stat.Mode), kind, permissions) ||
		(kind == RuntimePathRegular && stat.Nlink != 1) {
		return nil, ErrRuntimePathIdentity
	}
	return pinExpectedRuntimePathAt(directoryDescriptor, name, anchorName, identity, kind, permissions)
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
