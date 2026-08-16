package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

const runtimeAttachmentGenerationLink = ".attachment.generation"

var errRuntimeAttachmentGenerationDiffers = errors.New("runtime attachment generation differs")

func createRuntimeAttachmentGeneration(
	runtimeRootDescriptor int,
	taskHandle string,
) (reporter.RuntimeSocketIdentity, [16]byte, error) {
	if runtimeRootDescriptor < 0 || taskHandle == "" {
		return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation authority is invalid")
	}
	for attempt := 0; attempt < 4; attempt++ {
		var generationID [16]byte
		if _, err := rand.Read(generationID[:]); err != nil {
			return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation is unavailable")
		}
		name := runtimeAttachmentGenerationName(generationID)
		if err := unix.Mkdirat(runtimeRootDescriptor, name, 0o700); errors.Is(err, unix.EEXIST) {
			continue
		} else if err != nil {
			return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation is unavailable")
		}
		generationDescriptor, err := unix.Openat(
			runtimeRootDescriptor, name,
			unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
		)
		if err != nil {
			return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation is unavailable")
		}
		anchorDescriptor, anchorErr := unix.Openat(
			generationDescriptor, runtimeAttachmentGenerationLink,
			unix.O_RDONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600,
		)
		var generationStat unix.Stat_t
		var anchorStat unix.Stat_t
		generationStatErr := unix.Fstat(generationDescriptor, &generationStat)
		anchorStatErr := unix.Fstat(anchorDescriptor, &anchorStat)
		identity, identityErr := runtimeAttachmentDescriptorIdentity(generationDescriptor)
		resultErr := errors.Join(
			anchorErr, generationStatErr, anchorStatErr, identityErr,
			unix.Fsync(anchorDescriptor), unix.Fsync(generationDescriptor), unix.Fsync(runtimeRootDescriptor),
			unix.Close(anchorDescriptor), unix.Close(generationDescriptor),
		)
		if resultErr != nil || generationStat.Mode&unix.S_IFMT != unix.S_IFDIR || generationStat.Mode&0o777 != 0o700 ||
			anchorStat.Mode&unix.S_IFMT != unix.S_IFREG || anchorStat.Mode&0o777 != 0o600 || anchorStat.Nlink != 1 {
			return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation is unsafe")
		}
		return identity, generationID, nil
	}
	return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation is unavailable")
}

func linkRuntimeAttachmentGeneration(
	pinned *pinnedTaskRuntimeDirectory,
	expected reporter.RuntimeSocketIdentity,
	generationID [16]byte,
) (reporter.RuntimeSocketIdentity, error) {
	if pinned == nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation differs")
	}
	generationDescriptor, anchorDescriptor, anchorStat, err := pinRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, expected, generationID,
	)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	linkErr := unix.Linkat(
		generationDescriptor, runtimeAttachmentGenerationLink,
		pinned.taskDescriptor, runtimeAttachmentGenerationLink, 0,
	)
	if linkErr != nil && !errors.Is(linkErr, unix.EEXIST) {
		_ = unix.Close(anchorDescriptor)
		_ = unix.Close(generationDescriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation cannot be linked")
	}
	var currentAnchor unix.Stat_t
	var currentLink unix.Stat_t
	anchorErr := unix.Fstat(anchorDescriptor, &currentAnchor)
	linkErr = unix.Fstatat(pinned.taskDescriptor, runtimeAttachmentGenerationLink, &currentLink, unix.AT_SYMLINK_NOFOLLOW)
	currentIdentity, identityErr := runtimeAttachmentDescriptorIdentity(generationDescriptor)
	if anchorErr != nil || linkErr != nil || identityErr != nil ||
		!sameRuntimeAttachmentExactGeneration(currentIdentity, expected) ||
		!runtimeAttachmentStatsSameNode(anchorStat, currentAnchor) ||
		!runtimeAttachmentStatsSameNode(anchorStat, currentLink) ||
		currentAnchor.Mode&unix.S_IFMT != unix.S_IFREG || currentAnchor.Mode&0o777 != 0o600 || currentAnchor.Nlink < 2 ||
		currentLink.Mode&unix.S_IFMT != unix.S_IFREG || currentLink.Mode&0o777 != 0o600 || currentLink.Nlink < 2 {
		_ = unix.Close(anchorDescriptor)
		_ = unix.Close(generationDescriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation differs")
	}
	if err := errors.Join(
		unix.Fsync(anchorDescriptor), unix.Fsync(pinned.taskDescriptor), unix.Fsync(generationDescriptor),
		unix.Fsync(pinned.runtimeRootDescriptor), unix.Close(anchorDescriptor), unix.Close(generationDescriptor),
	); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation cannot be synchronized")
	}
	return currentIdentity, nil
}

func runtimeAttachmentGenerationMatches(
	pinned *pinnedTaskRuntimeDirectory,
	expected reporter.RuntimeSocketIdentity,
	generationID [16]byte,
) bool {
	matches, _ := inspectRuntimeAttachmentGeneration(pinned, expected, generationID)
	return matches
}

func inspectRuntimeAttachmentGeneration(
	pinned *pinnedTaskRuntimeDirectory,
	expected reporter.RuntimeSocketIdentity,
	generationID [16]byte,
) (bool, error) {
	if pinned == nil {
		return false, errRuntimeAttachmentGenerationDiffers
	}
	generationDescriptor, anchorDescriptor, anchorStat, err := pinRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, expected, generationID,
	)
	if err != nil {
		return false, err
	}
	var linkStat unix.Stat_t
	linkErr := unix.Fstatat(
		pinned.taskDescriptor, runtimeAttachmentGenerationLink, &linkStat, unix.AT_SYMLINK_NOFOLLOW,
	)
	currentIdentity, identityErr := runtimeAttachmentDescriptorIdentity(generationDescriptor)
	closeErr := errors.Join(unix.Close(anchorDescriptor), unix.Close(generationDescriptor))
	if identityErr != nil || closeErr != nil {
		return false, errors.New("runtime attachment generation is unavailable")
	}
	if linkErr != nil {
		if errors.Is(linkErr, unix.ENOENT) || errors.Is(linkErr, unix.ENOTDIR) || errors.Is(linkErr, unix.ELOOP) {
			return false, errRuntimeAttachmentGenerationDiffers
		}
		return false, errors.New("runtime attachment generation is unavailable")
	}
	if !sameRuntimeAttachmentExactGeneration(currentIdentity, expected) ||
		!runtimeAttachmentStatsSameNode(anchorStat, linkStat) ||
		linkStat.Mode&unix.S_IFMT != unix.S_IFREG || linkStat.Mode&0o777 != 0o600 || linkStat.Nlink < 2 {
		return false, errRuntimeAttachmentGenerationDiffers
	}
	return true, nil
}

func runtimeAttachmentGenerationAvailable(
	runtimeRootDescriptor int,
	expected reporter.RuntimeSocketIdentity,
	generationID [16]byte,
) bool {
	generationDescriptor, anchorDescriptor, _, err := pinRuntimeAttachmentGeneration(
		runtimeRootDescriptor, expected, generationID,
	)
	if err != nil {
		return false
	}
	return errors.Join(unix.Close(anchorDescriptor), unix.Close(generationDescriptor)) == nil
}

func pinRuntimeAttachmentGeneration(
	runtimeRootDescriptor int,
	expected reporter.RuntimeSocketIdentity,
	generationID [16]byte,
) (int, int, unix.Stat_t, error) {
	if runtimeRootDescriptor < 0 || !expected.Valid() || !runtimeAttachmentGenerationIDValid(generationID) {
		return -1, -1, unix.Stat_t{}, errRuntimeAttachmentGenerationDiffers
	}
	generationDescriptor, err := unix.Openat(
		runtimeRootDescriptor, runtimeAttachmentGenerationName(generationID),
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			return -1, -1, unix.Stat_t{}, errRuntimeAttachmentGenerationDiffers
		}
		return -1, -1, unix.Stat_t{}, errors.New("runtime attachment generation is unavailable")
	}
	var generationStat unix.Stat_t
	generationStatErr := unix.Fstat(generationDescriptor, &generationStat)
	identity, identityErr := runtimeAttachmentDescriptorIdentity(generationDescriptor)
	if generationStatErr != nil || identityErr != nil {
		_ = unix.Close(generationDescriptor)
		return -1, -1, unix.Stat_t{}, errors.New("runtime attachment generation is unavailable")
	}
	if !sameRuntimeAttachmentExactGeneration(identity, expected) || generationStat.Mode&unix.S_IFMT != unix.S_IFDIR ||
		generationStat.Mode&0o777 != 0o700 {
		_ = unix.Close(generationDescriptor)
		return -1, -1, unix.Stat_t{}, errRuntimeAttachmentGenerationDiffers
	}
	anchorDescriptor, err := unix.Openat(
		generationDescriptor, runtimeAttachmentGenerationLink,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		_ = unix.Close(generationDescriptor)
		if errors.Is(err, unix.ENOENT) || errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			return -1, -1, unix.Stat_t{}, errRuntimeAttachmentGenerationDiffers
		}
		return -1, -1, unix.Stat_t{}, errors.New("runtime attachment generation is unavailable")
	}
	var anchorStat unix.Stat_t
	currentIdentity, currentIdentityErr := runtimeAttachmentDescriptorIdentity(generationDescriptor)
	if err := unix.Fstat(anchorDescriptor, &anchorStat); err != nil || currentIdentityErr != nil {
		_ = unix.Close(anchorDescriptor)
		_ = unix.Close(generationDescriptor)
		return -1, -1, unix.Stat_t{}, errors.New("runtime attachment generation is unavailable")
	}
	if !sameRuntimeAttachmentExactGeneration(currentIdentity, expected) ||
		anchorStat.Mode&unix.S_IFMT != unix.S_IFREG || anchorStat.Mode&0o777 != 0o600 || anchorStat.Nlink < 1 {
		_ = unix.Close(anchorDescriptor)
		_ = unix.Close(generationDescriptor)
		return -1, -1, unix.Stat_t{}, errRuntimeAttachmentGenerationDiffers
	}
	return generationDescriptor, anchorDescriptor, anchorStat, nil
}

func sameRuntimeAttachmentExactGeneration(left, right reporter.RuntimeSocketIdentity) bool {
	return sameRuntimeAttachmentStableDirectory(left, right)
}

func runtimeAttachmentStatsSameNode(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino
}

func runtimeAttachmentGenerationIDValid(generationID [16]byte) bool {
	var nonzero byte
	for _, value := range generationID {
		nonzero |= value
	}
	return nonzero != 0
}

func runtimeAttachmentGenerationName(generationID [16]byte) string {
	return ".dg-" + hex.EncodeToString(generationID[:])
}
