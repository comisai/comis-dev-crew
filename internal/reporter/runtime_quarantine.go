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

// QuarantineRuntimePath atomically isolates and preserves one exact child identity.
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
	afterPin func() error
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
		return errors.Join(err, unix.Close(isolationDescriptor))
	}
	if hooks.afterPin != nil {
		if err := hooks.afterPin(); err != nil {
			return errors.Join(err, preserveRuntimeRemovalPin(targetDescriptor, kind), unix.Close(isolationDescriptor))
		}
	}
	if err := renameRuntimePathNoReplaceBetween(
		directoryDescriptor, name, isolationDescriptor, runtimePathIsolationTarget,
	); err != nil {
		closeErr := preserveRuntimeRemovalPin(targetDescriptor, kind)
		isolationErr := unix.Close(isolationDescriptor)
		if errors.Is(err, unix.ENOENT) {
			return errors.Join(ErrRuntimePathMissing, closeErr, isolationErr)
		}
		return errors.Join(errors.New("runtime path cannot be quarantined"), closeErr, isolationErr)
	}
	if err := errors.Join(syncRuntimeDirectory(directoryDescriptor), unix.Fsync(isolationDescriptor)); err != nil {
		return preserveIsolatedRuntimePathFailure(
			directoryDescriptor, isolationDescriptor, targetDescriptor, kind,
			errors.New("runtime path quarantine cannot be synchronized"),
		)
	}
	if afterQuarantine != nil {
		var pinnedStat unix.Stat_t
		pinnedErr := statRuntimeRemovalPin(targetDescriptor, &pinnedStat)
		pinnedIdentity, identityErr := runtimeRemovalPinIdentity(targetDescriptor, pinnedStat)
		if pinnedErr != nil || identityErr != nil {
			return preserveIsolatedRuntimePathFailure(
				directoryDescriptor, isolationDescriptor, targetDescriptor, kind,
				errors.New("runtime path pinned identity is unavailable"),
			)
		}
		if err := afterQuarantine(pinnedIdentity); err != nil {
			return preserveIsolatedRuntimePathFailure(
				directoryDescriptor, isolationDescriptor, targetDescriptor, kind, err,
			)
		}
	}
	if err := verifyPinnedRuntimePath(
		isolationDescriptor, runtimePathIsolationTarget, targetDescriptor, kind, permissions,
	); err != nil {
		return preserveIsolatedRuntimePathFailure(
			directoryDescriptor, isolationDescriptor, targetDescriptor, kind,
			errors.Join(ErrRuntimePathIdentity, err),
		)
	}
	return preserveIsolatedRuntimePath(isolationDescriptor, targetDescriptor, kind)
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
		var original unix.Stat_t
		originalErr := unix.Fstatat(directoryDescriptor, originalName, &original, unix.AT_SYMLINK_NOFOLLOW)
		if originalErr == nil {
			return false, unix.Close(isolationDescriptor)
		}
		if !errors.Is(originalErr, unix.ENOENT) {
			return true, errors.Join(ErrRuntimePathIdentity, unix.Close(isolationDescriptor))
		}
		return true, unix.Close(isolationDescriptor)
	}
	if err != nil {
		return true, errors.Join(err, unix.Close(isolationDescriptor))
	}
	return true, preserveIsolatedRuntimePath(isolationDescriptor, descriptor, kind)
}

func preserveIsolatedRuntimePath(
	isolationDescriptor int,
	targetDescriptor *runtimeRemovalPin,
	kind RuntimePathKind,
) error {
	return errors.Join(preserveRuntimeRemovalPin(targetDescriptor, kind), unix.Fsync(isolationDescriptor),
		unix.Close(isolationDescriptor))
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
	if created && syncRuntimeDirectory(directoryDescriptor) != nil {
		return -1, false, errors.Join(
			errors.New("runtime path isolation cannot be synchronized"), unix.Close(descriptor),
		)
	}
	return descriptor, created, nil
}

func preserveIsolatedRuntimePathFailure(
	directoryDescriptor int,
	isolationDescriptor int,
	targetDescriptor *runtimeRemovalPin,
	kind RuntimePathKind,
	cause error,
) error {
	return errors.Join(cause, ErrRuntimePathIdentity, errors.New("runtime path remains isolated"),
		unix.Fsync(isolationDescriptor), syncRuntimeDirectory(directoryDescriptor),
		preserveRuntimeRemovalPin(targetDescriptor, kind), unix.Close(isolationDescriptor))
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

func preserveMovedRuntimePathFailure(
	directoryDescriptor int,
	targetDescriptor *runtimeRemovalPin,
	kind RuntimePathKind,
	cause error,
) error {
	return errors.Join(cause, ErrRuntimePathIdentity, errors.New("runtime path remains moved"),
		syncRuntimeDirectory(directoryDescriptor), preserveRuntimeRemovalPin(targetDescriptor, kind))
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
