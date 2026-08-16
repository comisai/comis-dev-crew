package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

func runtimeAttachmentIdentityName(taskHandle string) (string, error) {
	if domain.ValidateTaskHandle(taskHandle) != nil {
		return "", errors.New("runtime attachment identity record name is invalid")
	}
	return taskHandle + runtimeAttachmentIdentitySuffix, nil
}

func runtimeAttachmentIdentityTemporaryName(
	taskHandle string,
	record runtimeAttachmentIdentityRecord,
) (string, error) {
	name, err := runtimeAttachmentIdentityName(taskHandle)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(formatRuntimeAttachmentIdentityRecord(record)))
	return name + ".new-" + hex.EncodeToString(digest[:16]), nil
}

func persistPinnedRuntimeAttachmentIdentity(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
	beforePublish func(),
) error {
	current, mode, found, err := readPinnedRuntimeSocketIdentity(pinned.taskDescriptor)
	if err != nil || !found || current != record.Socket || mode&unix.S_IFMT != unix.S_IFSOCK || mode&0o777 != 0o600 {
		return errors.New("persist runtime attachment identity: prepared socket identity differs")
	}
	prior, _, priorFound, err := readRuntimeAttachmentIdentityRecord(pinned.runtimeRootDescriptor, pinned.taskHandle)
	if err != nil {
		return err
	}
	var priorRecord *runtimeAttachmentIdentityRecord
	if priorFound {
		priorRecord = &prior
	}
	_, err = publishPinnedRuntimeAttachmentIdentity(pinned, record, priorRecord, beforePublish)
	return err
}

func publishPinnedRuntimeAttachmentIdentity(
	pinned *pinnedTaskRuntimeDirectory,
	record runtimeAttachmentIdentityRecord,
	priorRecord *runtimeAttachmentIdentityRecord,
	beforePublish func(),
) (reporter.RuntimeSocketIdentity, error) {
	return publishRuntimeAttachmentIdentity(
		pinned.runtimeRootDescriptor, pinned.taskHandle, record, priorRecord, beforePublish,
	)
}

func publishRuntimeAttachmentIdentity(
	runtimeRootDescriptor int,
	taskHandle string,
	record runtimeAttachmentIdentityRecord,
	priorRecord *runtimeAttachmentIdentityRecord,
	beforePublish func(),
) (reporter.RuntimeSocketIdentity, error) {
	name, err := runtimeAttachmentIdentityName(taskHandle)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	existingRecord, existingIdentity, existingFound, err := readRuntimeAttachmentIdentityRecord(
		runtimeRootDescriptor, taskHandle,
	)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	if existingFound != (priorRecord != nil) || existingFound && existingRecord != *priorRecord {
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: prior record differs")
	}
	if existingFound && existingRecord == record && beforePublish == nil {
		return existingIdentity, nil
	}
	temporaryName, err := runtimeAttachmentIdentityTemporaryName(taskHandle, record)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	temporaryIdentity, err := prepareRuntimeAttachmentIdentityTemporary(
		runtimeRootDescriptor, temporaryName, record,
	)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, err
	}
	cleanupTemporary := func(resultErr error) error {
		cleanupErr := reporter.QuarantineRuntimePath(
			runtimeRootDescriptor, temporaryName, temporaryIdentity, reporter.RuntimePathRegular, 0o600,
		)
		if errors.Is(cleanupErr, reporter.ErrRuntimePathMissing) {
			cleanupErr = nil
		}
		return errors.Join(resultErr, cleanupErr)
	}
	if beforePublish != nil {
		beforePublish()
	}
	if existingFound {
		if err := reporter.ReplaceRuntimePath(
			runtimeRootDescriptor, temporaryName, name, temporaryIdentity, existingIdentity, 0o600,
		); err != nil {
			return reporter.RuntimeSocketIdentity{}, cleanupTemporary(errors.New("persist runtime attachment identity: prior record changed"))
		}
		cleanupErr := reporter.QuarantineRuntimePath(
			runtimeRootDescriptor, temporaryName, existingIdentity, reporter.RuntimePathRegular, 0o600,
		)
		if cleanupErr != nil && !errors.Is(cleanupErr, reporter.ErrRuntimePathMissing) {
			return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: prior record cleanup failed")
		}
	} else if err := reporter.PublishRuntimePath(
		runtimeRootDescriptor, temporaryName, name, temporaryIdentity, 0o600,
	); err != nil {
		return reporter.RuntimeSocketIdentity{}, cleanupTemporary(errors.New("persist runtime attachment identity: record publication failed"))
	}
	published, publishedIdentity, found, err := readRuntimeAttachmentIdentityRecord(
		runtimeRootDescriptor, taskHandle,
	)
	if err != nil || !found || published != record {
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: published record differs")
	}
	return publishedIdentity, nil
}

func prepareRuntimeAttachmentIdentityTemporary(
	runtimeRootDescriptor int,
	temporaryName string,
	record runtimeAttachmentIdentityRecord,
) (reporter.RuntimeSocketIdentity, error) {
	encoded := formatRuntimeAttachmentIdentityRecord(record)
	descriptor, err := unix.Openat(
		runtimeRootDescriptor, temporaryName,
		unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600,
	)
	if errors.Is(err, unix.EEXIST) {
		return readRuntimeAttachmentIdentityTemporary(runtimeRootDescriptor, temporaryName, encoded)
	}
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: temporary record is unavailable")
	}
	file := os.NewFile(uintptr(descriptor), temporaryName)
	if file == nil {
		_ = unix.Close(descriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: temporary record is unavailable")
	}
	written, writeErr := file.WriteString(encoded)
	syncErr := file.Sync()
	var stat unix.Stat_t
	statErr := unix.Fstat(descriptor, &stat)
	identity, identityErr := runtimeAttachmentStatIdentity(stat)
	closeErr := file.Close()
	if writeErr != nil || written != len(encoded) || syncErr != nil || statErr != nil || identityErr != nil ||
		stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Nlink != 1 || closeErr != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: temporary record write failed")
	}
	return identity, nil
}

func readRuntimeAttachmentIdentityTemporary(
	runtimeRootDescriptor int,
	temporaryName, expected string,
) (reporter.RuntimeSocketIdentity, error) {
	descriptor, err := unix.Openat(
		runtimeRootDescriptor, temporaryName, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0,
	)
	if err != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: temporary record is unavailable")
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(descriptor, &stat)
	identity, identityErr := runtimeAttachmentStatIdentity(stat)
	file := os.NewFile(uintptr(descriptor), temporaryName)
	if file == nil {
		_ = unix.Close(descriptor)
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: temporary record is unavailable")
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, int64(len(expected)+1)))
	closeErr := file.Close()
	if statErr != nil || identityErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Nlink != 1 || readErr != nil || string(encoded) != expected || closeErr != nil {
		return reporter.RuntimeSocketIdentity{}, errors.New("persist runtime attachment identity: temporary record is unsafe")
	}
	return identity, nil
}

func formatRuntimeAttachmentIdentityRecord(record runtimeAttachmentIdentityRecord) string {
	return fmt.Sprintf(
		"%02x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x\n",
		record.Stage,
		record.Task.Device, record.Task.Inode, uint64(record.Task.ChangeSec), uint64(record.Task.ChangeNsec),
		uint64(record.Task.BirthSec), uint64(record.Task.BirthNsec),
		record.Socket.Device, record.Socket.Inode, uint64(record.Socket.ChangeSec), uint64(record.Socket.ChangeNsec),
		uint64(record.Socket.BirthSec), uint64(record.Socket.BirthNsec),
	)
}

func parseRuntimeAttachmentIdentityRecord(encoded string) (runtimeAttachmentIdentityRecord, error) {
	if len(encoded) != 207 || encoded[len(encoded)-1] != '\n' {
		return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
	}
	parts := strings.Split(encoded[:len(encoded)-1], ":")
	if len(parts) != 13 || len(parts[0]) != 2 {
		return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
	}
	values := make([]uint64, len(parts))
	for index, part := range parts {
		if index == 0 {
			value, err := strconv.ParseUint(part, 16, 8)
			if err != nil {
				return runtimeAttachmentIdentityRecord{}, errors.New("runtime attachment identity record is invalid")
			}
			values[index] = value
			continue
		}
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
		Stage: runtimeAttachmentIdentityStage(values[0]),
		Task: reporter.RuntimeSocketIdentity{
			Device: values[1], Inode: values[2], ChangeSec: int64(values[3]), ChangeNsec: int64(values[4]),
			BirthSec: int64(values[5]), BirthNsec: int64(values[6]),
		},
		Socket: reporter.RuntimeSocketIdentity{
			Device: values[7], Inode: values[8], ChangeSec: int64(values[9]), ChangeNsec: int64(values[10]),
			BirthSec: int64(values[11]), BirthNsec: int64(values[12]),
		},
	}
	valid := record.Stage == runtimeAttachmentCreatingIntent && record.Task == (reporter.RuntimeSocketIdentity{}) &&
		record.Socket == (reporter.RuntimeSocketIdentity{}) ||
		record.Stage == runtimeAttachmentCreating && record.Task.Valid() &&
			(record.Socket == (reporter.RuntimeSocketIdentity{}) || record.Socket.Valid()) ||
		(record.Stage == runtimeAttachmentActive || record.Stage == runtimeAttachmentReleasing) &&
			record.Task.Valid() && record.Socket.Valid()
	if !valid {
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
	var stat unix.Stat_t
	statErr := unix.Fstat(descriptor, &stat)
	recordIdentity, recordIdentityErr := runtimeAttachmentStatIdentity(stat)
	file := os.NewFile(uintptr(descriptor), name)
	if file == nil {
		_ = unix.Close(descriptor)
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unavailable")
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, 208))
	closeErr := file.Close()
	if statErr != nil || recordIdentityErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Nlink != 1 || readErr != nil || len(encoded) > 207 || closeErr != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("runtime attachment identity record is unsafe")
	}
	record, err := parseRuntimeAttachmentIdentityRecord(string(encoded))
	if err != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, err
	}
	return record, recordIdentity, true, nil
}
