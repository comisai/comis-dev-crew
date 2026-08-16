package service

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

const runtimeAttachmentIdentityName = "attachment.identity"

type pinnedTaskRuntimeDirectory struct {
	runtimeRootDescriptor int
	taskDescriptor        int
	taskHandle            string
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
	return runtimeAttachmentStatIdentity(stat)
}

func runtimeAttachmentPathIdentity(path string) (reporter.RuntimeSocketIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment filesystem identity is unavailable")
	}
	return runtimeAttachmentStatIdentity(stat)
}

func runtimeAttachmentStatIdentity(stat unix.Stat_t) (reporter.RuntimeSocketIdentity, error) {
	identity := reporter.RuntimeSocketIdentity{
		Device: uint64(stat.Dev), Inode: stat.Ino,
		ChangeSec: stat.Ctim.Sec, ChangeNsec: stat.Ctim.Nsec,
	}
	if !identity.Valid() {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment filesystem identity is invalid")
	}
	return identity, nil
}

func (coordinator *runtimeAttachmentCoordinator) pinTaskRuntimeDirectory(
	taskHandle string,
) (*pinnedTaskRuntimeDirectory, bool, error) {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return nil, false, errors.New("task runtime directory identity is invalid")
	}
	runtimeRootDescriptor, rootIdentity, err := pinRuntimeAttachmentDirectory(coordinator.runtimeRoot)
	if err != nil {
		return nil, false, err
	}
	if rootIdentity.Device != coordinator.runtimeRootIdentity.Device || rootIdentity.Inode != coordinator.runtimeRootIdentity.Inode {
		_ = unix.Close(runtimeRootDescriptor)
		return nil, false, errors.New("task runtime directory root identity changed")
	}
	taskDescriptor, err := unix.Openat(
		runtimeRootDescriptor,
		taskHandle,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if errors.Is(err, unix.ENOENT) {
		_ = unix.Close(runtimeRootDescriptor)
		return nil, true, nil
	}
	if err != nil {
		_ = unix.Close(runtimeRootDescriptor)
		return nil, false, errors.New("task runtime directory identity is unavailable")
	}
	taskIdentity, err := runtimeAttachmentDescriptorIdentity(taskDescriptor)
	if err != nil {
		_ = unix.Close(taskDescriptor)
		_ = unix.Close(runtimeRootDescriptor)
		return nil, false, err
	}
	var stat unix.Stat_t
	if err := unix.Fstat(taskDescriptor, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o777 != 0o700 {
		_ = unix.Close(taskDescriptor)
		_ = unix.Close(runtimeRootDescriptor)
		return nil, false, errors.New("task runtime directory is unsafe")
	}
	return &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, taskIdentity: taskIdentity,
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
	resultErr := persistPinnedRuntimeAttachmentIdentity(pinned, identity)
	return errors.Join(resultErr, pinned.close())
}

func persistPinnedRuntimeAttachmentIdentity(
	pinned *pinnedTaskRuntimeDirectory,
	identity reporter.RuntimeSocketIdentity,
) error {
	current, mode, found, err := readPinnedRuntimeSocketIdentity(pinned.taskDescriptor)
	if err != nil || !found || current != identity || mode&unix.S_IFMT != unix.S_IFSOCK || mode&0o777 != 0o600 {
		return errors.New("persist runtime attachment identity: prepared socket identity differs")
	}
	var existing unix.Stat_t
	if err := unix.Fstatat(pinned.taskDescriptor, runtimeAttachmentIdentityName, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if existing.Mode&unix.S_IFMT != unix.S_IFREG || existing.Mode&0o777 != 0o600 {
			return errors.New("persist runtime attachment identity: record is unsafe")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return errors.New("persist runtime attachment identity: record is unavailable")
	}
	descriptor, err := unix.Openat(
		pinned.taskDescriptor,
		runtimeAttachmentIdentityName,
		unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return errors.New("persist runtime attachment identity: record is unavailable")
	}
	var recordStat unix.Stat_t
	if err := unix.Fstat(descriptor, &recordStat); err != nil || recordStat.Mode&unix.S_IFMT != unix.S_IFREG || recordStat.Mode&0o777 != 0o600 {
		_ = unix.Close(descriptor)
		return errors.New("persist runtime attachment identity: record is unsafe")
	}
	file := os.NewFile(uintptr(descriptor), runtimeAttachmentIdentityName)
	if file == nil {
		_ = unix.Close(descriptor)
		return errors.New("persist runtime attachment identity: record is unavailable")
	}
	encoded := formatRuntimeAttachmentIdentity(identity)
	written, writeErr := file.WriteString(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || written != len(encoded) || syncErr != nil || closeErr != nil || unix.Fsync(pinned.taskDescriptor) != nil {
		return errors.New("persist runtime attachment identity: record write failed")
	}
	return nil
}

func formatRuntimeAttachmentIdentity(identity reporter.RuntimeSocketIdentity) string {
	return fmt.Sprintf("%016x:%016x:%016x:%016x\n", identity.Device, identity.Inode, uint64(identity.ChangeSec), uint64(identity.ChangeNsec))
}

func parseRuntimeAttachmentIdentity(encoded string) (reporter.RuntimeSocketIdentity, error) {
	if len(encoded) != 68 || encoded[len(encoded)-1] != '\n' {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment identity record is invalid")
	}
	parts := strings.Split(encoded[:len(encoded)-1], ":")
	if len(parts) != 4 {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment identity record is invalid")
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		if len(part) != 16 {
			return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment identity record is invalid")
		}
		value, err := strconv.ParseUint(part, 16, 64)
		if err != nil {
			return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment identity record is invalid")
		}
		values[index] = value
	}
	identity := reporter.RuntimeSocketIdentity{
		Device: values[0], Inode: values[1], ChangeSec: int64(values[2]), ChangeNsec: int64(values[3]),
	}
	if !identity.Valid() {
		return reporter.RuntimeSocketIdentity{}, errors.New("runtime attachment identity record is invalid")
	}
	return identity, nil
}

func readPinnedRuntimeAttachmentIdentity(
	pinned *pinnedTaskRuntimeDirectory,
) (reporter.RuntimeSocketIdentity, reporter.RuntimeSocketIdentity, bool, error) {
	descriptor, err := unix.Openat(
		pinned.taskDescriptor,
		runtimeAttachmentIdentityName,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if errors.Is(err, unix.ENOENT) {
		return reporter.RuntimeSocketIdentity{}, reporter.RuntimeSocketIdentity{}, false, nil
	}
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unavailable")
	}
	var recordStat unix.Stat_t
	statErr := unix.Fstat(descriptor, &recordStat)
	recordIdentity, recordIdentityErr := runtimeAttachmentStatIdentity(recordStat)
	file := os.NewFile(uintptr(descriptor), runtimeAttachmentIdentityName)
	if file == nil {
		_ = unix.Close(descriptor)
		return reporter.RuntimeSocketIdentity{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unavailable")
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, 69))
	closeErr := file.Close()
	if statErr != nil || recordIdentityErr != nil || recordStat.Mode&unix.S_IFMT != unix.S_IFREG || recordStat.Mode&0o777 != 0o600 ||
		readErr != nil || len(encoded) > 68 || closeErr != nil {
		return reporter.RuntimeSocketIdentity{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unsafe")
	}
	identity, err := parseRuntimeAttachmentIdentity(string(encoded))
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, reporter.RuntimeSocketIdentity{}, false, err
	}
	return identity, recordIdentity, true, nil
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

func (coordinator *runtimeAttachmentCoordinator) removeTaskRuntimeDirectory(taskHandle string) error {
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		return err
	}
	resultErr := removePinnedTaskRuntimeDirectory(pinned)
	return errors.Join(resultErr, pinned.close())
}

func removePinnedTaskRuntimeDirectory(pinned *pinnedTaskRuntimeDirectory) error {
	expected, recordIdentity, identityFound, err := readPinnedRuntimeAttachmentIdentity(pinned)
	if err != nil {
		return err
	}
	_, _, socketFound, err := readPinnedRuntimeSocketIdentity(pinned.taskDescriptor)
	if err != nil {
		return err
	}
	if socketFound {
		if !identityFound {
			return errors.New("task runtime attachment identity differs; path preserved")
		}
		if err := reporter.QuarantineRuntimePath(
			pinned.taskDescriptor, "attachment.sock", expected, reporter.RuntimePathSocket, 0o600,
		); err != nil && !errors.Is(err, reporter.ErrRuntimePathMissing) {
			return errors.New("task runtime attachment cannot be removed")
		}
	}
	if identityFound {
		if err := reporter.QuarantineRuntimePath(
			pinned.taskDescriptor, runtimeAttachmentIdentityName, recordIdentity, reporter.RuntimePathRegular, 0o600,
		); err != nil {
			return errors.New("runtime attachment identity record cannot be removed")
		}
	}
	if err := unix.Fsync(pinned.taskDescriptor); err != nil {
		return errors.New("task runtime directory update cannot be synchronized")
	}
	taskIdentity, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || taskIdentity.Device != pinned.taskIdentity.Device || taskIdentity.Inode != pinned.taskIdentity.Inode {
		return errors.New("task runtime directory identity is unavailable")
	}
	if err := reporter.QuarantineRuntimePath(
		pinned.runtimeRootDescriptor, pinned.taskHandle, taskIdentity, reporter.RuntimePathDirectory, 0o700,
	); err != nil {
		return errors.New("task runtime directory is not empty or unavailable")
	}
	return nil
}
