package service

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

const runtimeAttachmentIdentitySuffix = ".attachment.identity"

type runtimeAttachmentIdentityStage uint8

const (
	runtimeAttachmentCreatingIntent runtimeAttachmentIdentityStage = 1
	runtimeAttachmentCreating       runtimeAttachmentIdentityStage = 2
	runtimeAttachmentActive         runtimeAttachmentIdentityStage = 3
	runtimeAttachmentReleasing      runtimeAttachmentIdentityStage = 4
	runtimeAttachmentReleaseIntent  runtimeAttachmentIdentityStage = 5
	runtimeAttachmentDirectoryBound runtimeAttachmentIdentityStage = 6
)

type runtimeAttachmentIdentityRecord struct {
	Stage        runtimeAttachmentIdentityStage
	Task         reporter.RuntimeSocketIdentity
	Socket       reporter.RuntimeSocketIdentity
	Generation   reporter.RuntimeSocketIdentity
	GenerationID [16]byte
	RelaySeed    [32]byte
}

type pinnedTaskRuntimeDirectory struct {
	runtimeRootDescriptor int
	taskDescriptor        int
	taskHandle            string
	directoryName         string
	taskIdentity          reporter.RuntimeSocketIdentity
}

func runtimeAttachmentDirectoryIdentity(path string) (reporter.RuntimeSocketIdentity, error) {
	descriptor, identity, err := pinRuntimeAttachmentDirectory(path)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	if err := unix.Close(descriptor); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment directory identity release failed")
	}
	return identity, nil
}

func pinRuntimeAttachmentDirectory(path string) (int, reporter.RuntimeSocketIdentity, error) {
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	descriptor, err := unix.Open(string(os.PathSeparator), flags, 0)
	if err != nil {
		return -1, reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment directory identity is unavailable")
	}
	root := filepath.VolumeName(path) + string(os.PathSeparator)
	for _, component := range strings.Split(strings.TrimPrefix(path, root), string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		next, openErr := unix.Openat(descriptor, component, flags, 0)
		closeErr := unix.Close(descriptor)
		if openErr != nil || closeErr != nil {
			if next >= 0 {
				_ = unix.Close(next)
			}
			return -1, reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment directory identity is unavailable")
		}
		descriptor = next
	}
	identity, err := runtimeAttachmentDescriptorIdentity(descriptor)
	if err != nil {
		_ = unix.Close(descriptor)
		return -1, reporter.RuntimeSocketIdentity{}, err
	}
	return descriptor, identity, nil
}

func runtimeAttachmentDescriptorIdentity(descriptor int) (reporter.RuntimeSocketIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment filesystem identity is unavailable")
	}
	identity, err := runtimeAttachmentStatIdentity(stat)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	birthSec, birthNsec := runtimeAttachmentDescriptorBirthTime(descriptor)
	if birthSec != 0 || birthNsec != 0 {
		identity.BirthSec = birthSec
		identity.BirthNsec = birthNsec
	}
	return identity, nil
}

func runtimeAttachmentPathIdentity(path string) (reporter.RuntimeSocketIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment filesystem identity is unavailable")
	}
	return runtimeAttachmentStatIdentity(stat)
}

func runtimeAttachmentStatIdentity(stat unix.Stat_t) (reporter.RuntimeSocketIdentity, error) {
	birthSec, birthNsec := runtimeAttachmentStatBirthTime(stat)
	identity := reporter.RuntimeSocketIdentity{
		Device: uint64(stat.Dev), Inode: stat.Ino,
		ChangeSec: stat.Ctim.Sec, ChangeNsec: stat.Ctim.Nsec,
		BirthSec: birthSec, BirthNsec: birthNsec,
	}
	if !identity.Valid() {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment filesystem identity is invalid")
	}
	return identity, nil
}

func (coordinator *runtimeAttachmentCoordinator) pinRuntimeRoot() (int, error) {
	descriptor, identity, err := pinRuntimeAttachmentDirectory(coordinator.runtimeRoot)
	if err != nil {
		return -1, err
	}
	if !sameRuntimeAttachmentNode(identity, coordinator.runtimeRootIdentity) {
		_ = unix.Close(descriptor)
		return -1, errors.New("task runtime directory root identity changed")
	}
	return descriptor, nil
}

func openTaskRuntimeDirectory(
	runtimeRootDescriptor int,
	taskHandle string,
) (int, reporter.RuntimeSocketIdentity, bool, error) {
	taskDescriptor, err := unix.Openat(
		runtimeRootDescriptor,
		taskHandle,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if errors.Is(err, unix.ENOENT) {
		return -1, reporter.RuntimeSocketIdentity{}, true, nil
	}
	if err != nil {
		return -1, reporter.RuntimeSocketIdentity{}, false, errors.New("task runtime directory identity is unavailable")
	}
	identity, identityErr := runtimeAttachmentDescriptorIdentity(taskDescriptor)
	var stat unix.Stat_t
	statErr := unix.Fstat(taskDescriptor, &stat)
	if identityErr != nil || statErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 {
		_ = unix.Close(taskDescriptor)
		return -1, reporter.RuntimeSocketIdentity{}, false, errors.New("task runtime directory is unsafe")
	}
	return taskDescriptor, identity, false, nil
}

func (coordinator *runtimeAttachmentCoordinator) pinTaskRuntimeDirectory(
	taskHandle string,
) (*pinnedTaskRuntimeDirectory, bool, error) {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return nil, false, errors.New("task runtime directory identity is invalid")
	}
	runtimeRootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		return nil, false, err
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(runtimeRootDescriptor, taskHandle)
	if err != nil || missing {
		_ = unix.Close(runtimeRootDescriptor)
		return nil, missing, err
	}
	return &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, directoryName: taskHandle, taskIdentity: taskIdentity,
	}, false, nil
}

func (pinned *pinnedTaskRuntimeDirectory) close() error {
	taskErr := unix.Close(pinned.taskDescriptor)
	rootErr := unix.Close(pinned.runtimeRootDescriptor)
	if taskErr != nil || rootErr != nil {
		return errors.New("task runtime directory identity release failed")
	}
	return nil
}

func (coordinator *runtimeAttachmentCoordinator) persistRuntimeAttachmentIdentity(
	taskHandle string,
	identity reporter.RuntimeSocketIdentity,
) error {
	if !identity.Valid() {
		return errors.New("persist runtime attachment identity: identity is invalid")
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		return errors.New("persist runtime attachment identity: task directory is unavailable")
	}
	record := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentActive, Task: pinned.taskIdentity, Socket: identity}
	record.RelaySeed = sha256.Sum256([]byte("runtime-relay-test\x00" + taskHandle))
	record.Generation, record.GenerationID, err = createRuntimeAttachmentGeneration(pinned.runtimeRootDescriptor, taskHandle)
	if err != nil {
		return errors.Join(err, pinned.close())
	}
	record.Generation, err = linkRuntimeAttachmentGeneration(pinned, record.Generation, record.GenerationID)
	if err != nil {
		return errors.Join(err, pinned.close())
	}
	resultErr := persistPinnedRuntimeAttachmentIdentity(pinned, record, nil)
	return errors.Join(resultErr, pinned.close())
}

func readPinnedRuntimeSocketIdentity(
	taskDescriptor int,
) (reporter.RuntimeSocketIdentity, uint32, bool, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(taskDescriptor, "attachment.sock", &stat, unix.AT_SYMLINK_NOFOLLOW); errors.Is(err, unix.ENOENT) {
		return reporter.RuntimeSocketIdentity{}, 0, false, nil
	} else if err != nil {
		return reporter.RuntimeSocketIdentity{}, 0, false, errors.New("task runtime attachment is unavailable")
	}
	identity, err := runtimeAttachmentStatIdentity(stat)
	return identity, uint32(stat.Mode), true, err
}

func runtimeAttachmentPathAbsent(directoryDescriptor int, name string) bool {
	var stat unix.Stat_t
	return errors.Is(unix.Fstatat(directoryDescriptor, name, &stat, unix.AT_SYMLINK_NOFOLLOW), unix.ENOENT)
}

func runtimeAttachmentDirectoryEmpty(descriptor int) bool {
	duplicate, err := unix.Dup(descriptor)
	if err != nil {
		return false
	}
	directory := os.NewFile(uintptr(duplicate), "runtime-attachment-directory")
	if directory == nil {
		_ = unix.Close(duplicate)
		return false
	}
	names, readErr := directory.Readdirnames(1)
	closeErr := directory.Close()
	return len(names) == 0 && errors.Is(readErr, io.EOF) && closeErr == nil
}

func sameRuntimeAttachmentNode(left, right reporter.RuntimeSocketIdentity) bool {
	return left.Device == right.Device && left.Inode == right.Inode
}

func sameRuntimeAttachmentStableDirectory(left, right reporter.RuntimeSocketIdentity) bool {
	if !sameRuntimeAttachmentNode(left, right) {
		return false
	}
	if right.BirthSec != 0 || right.BirthNsec != 0 {
		return left.BirthSec == right.BirthSec && left.BirthNsec == right.BirthNsec
	}
	return left.ChangeSec == right.ChangeSec && left.ChangeNsec == right.ChangeNsec
}

func runtimeAttachmentTransitionDirectoryMatches(
	current, recorded reporter.RuntimeSocketIdentity,
) bool {
	if recorded.BirthSec == 0 && recorded.BirthNsec == 0 {
		return false
	}
	return sameRuntimeAttachmentNode(current, recorded) &&
		current.BirthSec == recorded.BirthSec && current.BirthNsec == recorded.BirthNsec
}

func runtimeAttachmentCreationName(taskHandle string) string {
	digest := sha256.Sum256([]byte(taskHandle))
	return fmt.Sprintf(".dc-%x", digest[:8])
}

func closeRuntimeRootDescriptor(descriptor int) error {
	if unix.Close(descriptor) != nil {
		return errors.New("task runtime directory identity release failed")
	}
	return nil
}
