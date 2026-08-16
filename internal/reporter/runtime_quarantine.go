package reporter

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
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
	return quarantineRuntimePath(directoryDescriptor, name, expected, kind, permissions, rand.Reader, nil)
}

func quarantineRuntimePath(
	directoryDescriptor int,
	name string,
	expected RuntimeSocketIdentity,
	kind RuntimePathKind,
	permissions os.FileMode,
	entropy io.Reader,
	afterQuarantine func() error,
) error {
	if directoryDescriptor < 0 || !validRuntimeRemovalName(name) || !expected.Valid() ||
		!validRuntimePathKind(kind) || permissions.Perm() != permissions || entropy == nil {
		return errors.New("runtime path removal authority is invalid")
	}
	random := make([]byte, 16)
	if _, err := io.ReadFull(entropy, random); err != nil {
		return errors.New("runtime path quarantine identity is unavailable")
	}
	var originalStat unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, name, &originalStat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return ErrRuntimePathMissing
		}
		return errors.New("runtime path identity is unavailable")
	}
	original, err := runtimeSocketStatIdentity(originalStat)
	if err != nil || original != expected || !runtimePathModeMatches(uint32(originalStat.Mode), kind, permissions) {
		return ErrRuntimePathIdentity
	}
	quarantine := ".devcrew-remove-" + hex.EncodeToString(random)
	if err := renameRuntimePathNoReplace(directoryDescriptor, name, quarantine); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return ErrRuntimePathMissing
		}
		return errors.New("runtime path cannot be quarantined")
	}
	if afterQuarantine != nil {
		if err := afterQuarantine(); err != nil {
			return restoreQuarantinedRuntimePath(directoryDescriptor, quarantine, name, err)
		}
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(directoryDescriptor, quarantine, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return restoreQuarantinedRuntimePath(
			directoryDescriptor, quarantine, name,
			errors.New("runtime path quarantine identity is unavailable"),
		)
	}
	current, err := runtimeSocketStatIdentity(stat)
	if err != nil || current.Device != expected.Device || current.Inode != expected.Inode ||
		!runtimePathModeMatches(uint32(stat.Mode), kind, permissions) {
		return restoreQuarantinedRuntimePath(directoryDescriptor, quarantine, name, ErrRuntimePathIdentity)
	}
	flags := 0
	if kind == RuntimePathDirectory {
		flags = unix.AT_REMOVEDIR
	}
	if err := unix.Unlinkat(directoryDescriptor, quarantine, flags); err != nil {
		return restoreQuarantinedRuntimePath(
			directoryDescriptor, quarantine, name,
			errors.New("runtime path quarantine cannot be removed"),
		)
	}
	return nil
}

func restoreQuarantinedRuntimePath(directoryDescriptor int, quarantine, name string, cause error) error {
	if err := renameRuntimePathNoReplace(directoryDescriptor, quarantine, name); err != nil {
		return errors.Join(cause, errors.New("runtime path remains quarantined"))
	}
	return cause
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
