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

const runtimeAttachmentIdentitySuffix = ".attachment.identity"

type runtimeAttachmentIdentityRecord struct {
	Task   reporter.RuntimeSocketIdentity
	Socket reporter.RuntimeSocketIdentity
}

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

func runtimeAttachmentIdentityName(taskHandle string) (string, error) {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return "", errors.New("runtime attachment identity record name is invalid")
	}
	return taskHandle + runtimeAttachmentIdentitySuffix, nil
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
	resultErr := persistPinnedRuntimeAttachmentIdentity(pinned, runtimeAttachmentIdentityRecord{
		Task: pinned.taskIdentity, Socket: identity,
	})
	return errors.Join(resultErr, pinned.close())
}

func persistPinnedRuntimeAttachmentIdentity(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) error {
	current, mode, found, err := readPinnedRuntimeSocketIdentity(pinned.taskDescriptor)
	if err != nil || !found || current != record.Socket || mode&unix.S_IFMT != unix.S_IFSOCK || mode&0o777 != 0o600 {
		return errors.New("persist runtime attachment identity: prepared socket identity differs")
	}
	name, err := runtimeAttachmentIdentityName(pinned.taskHandle)
	if err != nil {
		return err
	}
	var existing unix.Stat_t
	if err := unix.Fstatat(pinned.runtimeRootDescriptor, name, &existing, unix.AT_SYMLINK_NOFOLLOW); err == nil {
		if existing.Mode&unix.S_IFMT != unix.S_IFREG || existing.Mode&0o777 != 0o600 {
			return errors.New("persist runtime attachment identity: record is unsafe")
		}
	} else if !errors.Is(err, unix.ENOENT) {
		return errors.New("persist runtime attachment identity: record is unavailable")
	}
	descriptor, err := unix.Openat(
		pinned.runtimeRootDescriptor,
		name,
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
	file := os.NewFile(uintptr(descriptor), name)
	if file == nil {
		_ = unix.Close(descriptor)
		return errors.New("persist runtime attachment identity: record is unavailable")
	}
	encoded := formatRuntimeAttachmentIdentityRecord(record)
	written, writeErr := file.WriteString(encoded)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || written != len(encoded) || syncErr != nil || closeErr != nil || unix.Fsync(pinned.runtimeRootDescriptor) != nil {
		return errors.New("persist runtime attachment identity: record write failed")
	}
	return nil
}

func formatRuntimeAttachmentIdentityRecord(record runtimeAttachmentIdentityRecord) string {
	return fmt.Sprintf(
		"%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x\n",
		record.Task.Device, record.Task.Inode, uint64(record.Task.ChangeSec), uint64(record.Task.ChangeNsec),
		record.Socket.Device, record.Socket.Inode, uint64(record.Socket.ChangeSec), uint64(record.Socket.ChangeNsec),
	)
}

func parseRuntimeAttachmentIdentityRecord(encoded string) (runtimeAttachmentIdentityRecord, error) {
	if len(encoded) != 136 || encoded[len(encoded)-1] != '\n' {
		return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
	}
	parts := strings.Split(encoded[:len(encoded)-1], ":")
	if len(parts) != 8 {
		return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		if len(part) != 16 {
			return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
		}
		value, err := strconv.ParseUint(part, 16, 64)
		if err != nil {
			return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
		}
		values[index] = value
	}
	record := runtimeAttachmentIdentityRecord{
		Task: reporter.RuntimeSocketIdentity{
			Device: values[0], Inode: values[1], ChangeSec: int64(values[2]), ChangeNsec: int64(values[3]),
		},
		Socket: reporter.RuntimeSocketIdentity{
			Device: values[4], Inode: values[5], ChangeSec: int64(values[6]), ChangeNsec: int64(values[7]),
		},
	}
	if !record.Task.Valid() || !record.Socket.Valid() {
		return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
	}
	return record, nil
}

func readRuntimeAttachmentIdentityRecord(
	runtimeRootDescriptor int,
	taskHandle string,
) (runtimeAttachmentIdentityRecord, reporter.RuntimeSocketIdentity, bool, error) {
	name, err := runtimeAttachmentIdentityName(taskHandle)
	if err != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, err
	}
	descriptor, err := unix.Openat(runtimeRootDescriptor, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, nil
	}
	if err != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unavailable")
	}
	var recordStat unix.Stat_t
	statErr := unix.Fstat(descriptor, &recordStat)
	recordIdentity, recordIdentityErr := runtimeAttachmentStatIdentity(recordStat)
	file := os.NewFile(uintptr(descriptor), name)
	if file == nil {
		_ = unix.Close(descriptor)
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unavailable")
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, 137))
	closeErr := file.Close()
	if statErr != nil || recordIdentityErr != nil || recordStat.Mode&unix.S_IFMT != unix.S_IFREG || recordStat.Mode&0o777 != 0o600 ||
		readErr != nil || len(encoded) > 136 || closeErr != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unsafe")
	}
	record, err := parseRuntimeAttachmentIdentityRecord(string(encoded))
	if err != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, err
	}
	return record, recordIdentity, true, nil
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
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return errors.New("task runtime directory identity is invalid")
	}
	runtimeRootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		return err
	}
	record, recordIdentity, identityFound, err := readRuntimeAttachmentIdentityRecord(runtimeRootDescriptor, taskHandle)
	if err != nil {
		return errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	taskDescriptor, taskIdentity, taskMissing, err := openTaskRuntimeDirectory(runtimeRootDescriptor, taskHandle)
	if err != nil {
		return errors.Join(err, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	if !identityFound {
		if !taskMissing {
			_ = unix.Close(taskDescriptor)
			return errors.Join(
				errors.New("task runtime directory identity is unproven; path preserved"),
				closeRuntimeRootDescriptor(runtimeRootDescriptor),
			)
		}
		return closeRuntimeRootDescriptor(runtimeRootDescriptor)
	}
	name, nameErr := runtimeAttachmentIdentityName(taskHandle)
	if nameErr != nil {
		return errors.Join(nameErr, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	if taskMissing {
		removeErr := reporter.QuarantineRuntimePath(
			runtimeRootDescriptor, name, recordIdentity, reporter.RuntimePathRegular, 0o600,
		)
		if removeErr == nil {
			removeErr = unix.Fsync(runtimeRootDescriptor)
		}
		return errors.Join(removeErr, closeRuntimeRootDescriptor(runtimeRootDescriptor))
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: runtimeRootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, taskIdentity: taskIdentity,
	}
	if !sameRuntimeAttachmentNode(record.Task, taskIdentity) {
		return errors.Join(errors.New("task runtime directory identity differs; path preserved"), pinned.close())
	}
	resultErr := removePinnedTaskRuntimeDirectory(pinned, record)
	if resultErr == nil {
		resultErr = reporter.QuarantineRuntimePath(
			pinned.runtimeRootDescriptor, name, recordIdentity, reporter.RuntimePathRegular, 0o600,
		)
	}
	if resultErr == nil && unix.Fsync(pinned.runtimeRootDescriptor) != nil {
		resultErr = errors.New("runtime attachment identity record update cannot be synchronized")
	}
	return errors.Join(resultErr, pinned.close())
}

func removePinnedTaskRuntimeDirectory(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
) error {
	_, _, socketFound, err := readPinnedRuntimeSocketIdentity(pinned.taskDescriptor)
	if err != nil {
		return err
	}
	if socketFound {
		if err := reporter.QuarantineRuntimePath(
			pinned.taskDescriptor, "attachment.sock", record.Socket, reporter.RuntimePathSocket, 0o600,
		); err != nil && !errors.Is(err, reporter.ErrRuntimePathMissing) {
			return errors.New("task runtime attachment cannot be removed")
		}
	}
	if err := unix.Fsync(pinned.taskDescriptor); err != nil {
		return errors.New("task runtime directory update cannot be synchronized")
	}
	current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil || !sameRuntimeAttachmentNode(current, pinned.taskIdentity) {
		return errors.New("task runtime directory identity is unavailable")
	}
	if err := reporter.QuarantineRuntimePath(
		pinned.runtimeRootDescriptor, pinned.taskHandle, current, reporter.RuntimePathDirectory, 0o700,
	); err != nil {
		return errors.New("task runtime directory is not empty or unavailable")
	}
	return nil
}

func sameRuntimeAttachmentNode(left, right reporter.RuntimeSocketIdentity) bool {
	return left.Device == right.Device && left.Inode == right.Inode
}

func closeRuntimeRootDescriptor(descriptor int) error {
	if unix.Close(descriptor) != nil {
		return errors.New("task runtime directory identity release failed")
	}
	return nil
}
