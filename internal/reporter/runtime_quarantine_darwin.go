//go:build darwin

package reporter

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

type runtimeRemovalPin struct {
	descriptor          int
	directoryDescriptor int
	anchor              string
}

func renameRuntimePathNoReplace(directoryDescriptor int, from, to string) error {
	return unix.RenameatxNp(directoryDescriptor, from, directoryDescriptor, to, unix.RENAME_EXCL)
}

func renameRuntimePathNoReplaceBetween(fromDescriptor int, from string, toDescriptor int, to string) error {
	return unix.RenameatxNp(fromDescriptor, from, toDescriptor, to, unix.RENAME_EXCL)
}

func exchangeRuntimePaths(directoryDescriptor int, left, right string) error {
	return unix.RenameatxNp(directoryDescriptor, left, directoryDescriptor, right, unix.RENAME_SWAP)
}

func openRuntimeRemovalPath(
	directoryDescriptor int,
	name, anchorName string,
	anchorDirectoryDescriptor int,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
) (*runtimeRemovalPin, error) {
	if kind != RuntimePathSocket {
		descriptor, err := unix.Openat(directoryDescriptor, name, unix.O_EVTONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		return &runtimeRemovalPin{descriptor: descriptor}, nil
	}
	anchor := runtimeRemovalAnchorName(anchorName, expected)
	var original unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, name, &original, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, err
	}
	originalIdentity, err := runtimeSocketStatIdentity(original)
	if err != nil || !runtimeSocketIdentityMatches(originalIdentity, expected) ||
		!runtimePathModeMatches(uint32(original.Mode), kind, permissions) {
		return nil, ErrRuntimePathIdentity
	}
	linkErr := unix.Linkat(directoryDescriptor, name, anchorDirectoryDescriptor, anchor, 0)
	if linkErr != nil && !errors.Is(linkErr, unix.EEXIST) {
		return nil, linkErr
	}
	pin := &runtimeRemovalPin{descriptor: -1, directoryDescriptor: anchorDirectoryDescriptor, anchor: anchor}
	var anchored unix.Stat_t
	var current unix.Stat_t
	if unix.Fstatat(anchorDirectoryDescriptor, anchor, &anchored, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		unix.Fstatat(directoryDescriptor, name, &current, unix.AT_SYMLINK_NOFOLLOW) != nil ||
		!runtimeStatsSameStableObject(anchored, current) {
		if linkErr == nil {
			_ = closeRuntimeRemovalPin(pin)
		}
		return nil, ErrRuntimePathIdentity
	}
	anchoredIdentity, err := runtimeSocketStatIdentity(anchored)
	if err != nil || !runtimeSocketIdentityMatches(anchoredIdentity, expected) || anchored.Nlink < 2 {
		if linkErr == nil {
			_ = closeRuntimeRemovalPin(pin)
		}
		return nil, ErrRuntimePathIdentity
	}
	return pin, nil
}

func statRuntimeRemovalPin(pin *runtimeRemovalPin, stat *unix.Stat_t) error {
	if pin.descriptor >= 0 {
		return unix.Fstat(pin.descriptor, stat)
	}
	return unix.Fstatat(pin.directoryDescriptor, pin.anchor, stat, unix.AT_SYMLINK_NOFOLLOW)
}

func runtimeRemovalPinIdentity(_ *runtimeRemovalPin, stat unix.Stat_t) (RuntimeSocketIdentity, error) {
	return runtimeSocketStatIdentity(stat)
}

func closeRuntimeRemovalPin(pin *runtimeRemovalPin) error {
	if pin.descriptor >= 0 {
		return unix.Close(pin.descriptor)
	}
	unlinkErr := unix.Unlinkat(pin.directoryDescriptor, pin.anchor, 0)
	syncErr := unix.Fsync(pin.directoryDescriptor)
	return errors.Join(unlinkErr, syncErr)
}

func runtimeRemovalAnchorName(name string, expected RuntimeSocketIdentity) string {
	encoded := runtimePathQuarantineName(name, expected, RuntimePathSocket, 0o600)
	digest := sha256.Sum256([]byte(encoded))
	return ".devcrew-pin-" + hex.EncodeToString(digest[:])
}
