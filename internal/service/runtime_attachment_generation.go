package service

import (
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

const runtimeAttachmentGenerationLink = ".attachment.generation"

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
		descriptor, err := unix.Openat(
			runtimeRootDescriptor, name,
			unix.O_RDONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600,
		)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err != nil {
			return reporter.RuntimeSocketIdentity{}, [16]byte{}, errors.New("runtime attachment generation is unavailable")
		}
		identity, identityErr := runtimeAttachmentDescriptorIdentity(descriptor)
		var stat unix.Stat_t
		statErr := unix.Fstat(descriptor, &stat)
		resultErr := errors.Join(identityErr, statErr, unix.Fsync(descriptor), unix.Close(descriptor), unix.Fsync(runtimeRootDescriptor))
		if resultErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Nlink != 1 {
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
	if pinned == nil || !expected.Valid() || !runtimeAttachmentGenerationIDValid(generationID) {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation differs")
	}
	name := runtimeAttachmentGenerationName(generationID)
	descriptor, err := unix.Openat(
		pinned.runtimeRootDescriptor, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation differs")
	}
	var pinnedStat unix.Stat_t
	pinnedStatErr := unix.Fstat(descriptor, &pinnedStat)
	pinnedIdentity, pinnedIdentityErr := runtimeAttachmentStatIdentity(pinnedStat)
	if pinnedStatErr != nil || pinnedIdentityErr != nil ||
		!sameRuntimeAttachmentExactGeneration(pinnedIdentity, expected) ||
		pinnedStat.Mode&unix.S_IFMT != unix.S_IFREG || pinnedStat.Mode&0o777 != 0o600 || pinnedStat.Nlink < 1 {
		_ = unix.Close(descriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation differs")
	}
	err = unix.Linkat(
		pinned.runtimeRootDescriptor, name, pinned.taskDescriptor, runtimeAttachmentGenerationLink, 0,
	)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		_ = unix.Close(descriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation cannot be linked")
	}
	var currentRoot unix.Stat_t
	var currentLink unix.Stat_t
	rootErr := unix.Fstatat(pinned.runtimeRootDescriptor, name, &currentRoot, unix.AT_SYMLINK_NOFOLLOW)
	linkErr := unix.Fstatat(pinned.taskDescriptor, runtimeAttachmentGenerationLink, &currentLink, unix.AT_SYMLINK_NOFOLLOW)
	currentIdentity, identityErr := runtimeAttachmentDescriptorIdentity(descriptor)
	if rootErr != nil || linkErr != nil || identityErr != nil ||
		!runtimeAttachmentStatsSameNode(pinnedStat, currentRoot) ||
		!runtimeAttachmentStatsSameNode(pinnedStat, currentLink) ||
		currentRoot.Mode&unix.S_IFMT != unix.S_IFREG || currentRoot.Mode&0o777 != 0o600 || currentRoot.Nlink < 2 ||
		currentLink.Mode&unix.S_IFMT != unix.S_IFREG || currentLink.Mode&0o777 != 0o600 || currentLink.Nlink < 2 {
		_ = unix.Close(descriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment generation differs")
	}
	if err := errors.Join(
		unix.Fsync(descriptor), unix.Fsync(pinned.taskDescriptor), unix.Fsync(pinned.runtimeRootDescriptor), unix.Close(descriptor),
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
	if pinned == nil || !runtimeAttachmentGenerationAvailable(
		pinned.runtimeRootDescriptor, expected, generationID,
	) {
		return false
	}
	descriptor, err := unix.Openat(
		pinned.taskDescriptor, runtimeAttachmentGenerationLink,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		return false
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(descriptor, &stat)
	identity, identityErr := runtimeAttachmentDescriptorIdentity(descriptor)
	closeErr := unix.Close(descriptor)
	if statErr != nil || identityErr != nil || closeErr != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Nlink < 2 {
		return false
	}
	return sameRuntimeAttachmentExactGeneration(identity, expected)
}

func runtimeAttachmentGenerationAvailable(
	runtimeRootDescriptor int,
	expected reporter.RuntimeSocketIdentity,
	generationID [16]byte,
) bool {
	if runtimeRootDescriptor < 0 || !expected.Valid() || !runtimeAttachmentGenerationIDValid(generationID) {
		return false
	}
	descriptor, err := unix.Openat(
		runtimeRootDescriptor, runtimeAttachmentGenerationName(generationID),
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		return false
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(descriptor, &stat)
	identity, identityErr := runtimeAttachmentDescriptorIdentity(descriptor)
	closeErr := unix.Close(descriptor)
	return statErr == nil && identityErr == nil && closeErr == nil &&
		stat.Mode&unix.S_IFMT == unix.S_IFREG && stat.Mode&0o777 == 0o600 && stat.Nlink >= 1 &&
		sameRuntimeAttachmentExactGeneration(identity, expected)
}

func sameRuntimeAttachmentExactGeneration(left, right reporter.RuntimeSocketIdentity) bool {
	if !sameRuntimeAttachmentNode(left, right) {
		return false
	}
	if left.BirthSec != 0 || left.BirthNsec != 0 || right.BirthSec != 0 || right.BirthNsec != 0 {
		return left.BirthSec == right.BirthSec && left.BirthNsec == right.BirthNsec
	}
	return left.ChangeSec == right.ChangeSec && left.ChangeNsec == right.ChangeNsec
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
