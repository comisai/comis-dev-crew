package service

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

func (coordinator *runtimeAttachmentCoordinator) recoverRuntimeRelayIdentityUpgrades(ctx context.Context) error {
	upgrades, err := coordinator.store.ListRuntimeRelayIdentityUpgrades(ctx)
	if err != nil {
		return errors.New("recover runtime relay identities: durable upgrades are unavailable")
	}
	for _, upgrade := range upgrades {
		if upgrade.Validate() != nil {
			return errors.New("recover runtime relay identities: durable upgrade is invalid")
		}
		descriptor, err := coordinator.pinRuntimeRoot()
		if err != nil {
			return err
		}
		record, _, found, readErr := readRuntimeAttachmentIdentityRecord(descriptor, upgrade.TaskHandle)
		if readErr == nil && found {
			prior := record
			record.RelaySeed = upgrade.RelaySeed
			_, err = publishRuntimeAttachmentIdentity(descriptor, upgrade.TaskHandle, record, &prior, nil)
		} else {
			err = upgradeLegacyRuntimeAttachmentIdentity(descriptor, upgrade)
		}
		closeErr := closeRuntimeRootDescriptor(descriptor)
		if err != nil || closeErr != nil {
			return errors.New("recover runtime relay identities: filesystem authority cannot be upgraded")
		}
		if err := coordinator.store.CompleteRuntimeRelayIdentityUpgrade(ctx, upgrade); err != nil {
			return errors.New("recover runtime relay identities: durable upgrade cannot be completed")
		}
	}
	return nil
}

func upgradeLegacyRuntimeAttachmentIdentity(
	runtimeRootDescriptor int,
	upgrade application.RuntimeRelayIdentityUpgrade,
) error {
	legacy, legacyIdentity, found, err := readLegacyRuntimeAttachmentIdentityRecord(
		runtimeRootDescriptor, upgrade.TaskHandle,
	)
	if err != nil || !found {
		return errors.New("legacy runtime relay identity authority is unavailable")
	}
	legacy.RelaySeed = upgrade.RelaySeed
	temporaryName, err := runtimeAttachmentIdentityTemporaryName(upgrade.TaskHandle)
	if err != nil {
		return err
	}
	temporaryIdentity, err := prepareRuntimeAttachmentIdentityTemporary(runtimeRootDescriptor, temporaryName, legacy)
	if err != nil {
		return err
	}
	name, err := runtimeAttachmentIdentityName(upgrade.TaskHandle)
	if err != nil {
		return err
	}
	if err := reporter.ReplaceRuntimePath(
		runtimeRootDescriptor, temporaryName, name, temporaryIdentity, legacyIdentity, 0o600,
	); err != nil {
		return errors.New("legacy runtime relay identity publication failed")
	}
	stored, _, found, err := readRuntimeAttachmentIdentityRecord(runtimeRootDescriptor, upgrade.TaskHandle)
	if err != nil || !found || stored != legacy {
		return errors.New("legacy runtime relay identity publication differs")
	}
	return nil
}

func readLegacyRuntimeAttachmentIdentityRecord(
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
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, err
	}
	var stat unix.Stat_t
	statErr := unix.Fstat(descriptor, &stat)
	identity, identityErr := runtimeAttachmentStatIdentity(stat)
	file := os.NewFile(uintptr(descriptor), name)
	if file == nil {
		_ = unix.Close(descriptor)
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("legacy runtime identity is unavailable")
	}
	encoded, readErr := io.ReadAll(io.LimitReader(file, 208))
	closeErr := file.Close()
	if statErr != nil || identityErr != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 ||
		stat.Nlink != 1 || readErr != nil || closeErr != nil {
		return runtimeAttachmentIdentityRecord{}, reporter.RuntimeSocketIdentity{}, false, errors.New("legacy runtime identity is unsafe")
	}
	record, err := parseLegacyRuntimeAttachmentIdentityRecord(string(encoded))
	return record, identity, true, err
}

func parseLegacyRuntimeAttachmentIdentityRecord(encoded string) (runtimeAttachmentIdentityRecord, error) {
	if len(encoded) != 207 || encoded[206] != '\n' {
		return runtimeAttachmentIdentityRecord{}, errors.New("legacy runtime identity is invalid")
	}
	parts := strings.Split(encoded[:206], ":")
	if len(parts) != 13 || len(parts[0]) != 2 {
		return runtimeAttachmentIdentityRecord{}, errors.New("legacy runtime identity is invalid")
	}
	values := make([]uint64, 13)
	for index, part := range parts {
		bits := 64
		if index == 0 {
			bits = 8
		} else if len(part) != 16 {
			return runtimeAttachmentIdentityRecord{}, errors.New("legacy runtime identity is invalid")
		}
		value, err := strconv.ParseUint(part, 16, bits)
		if err != nil {
			return runtimeAttachmentIdentityRecord{}, errors.New("legacy runtime identity is invalid")
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
		return runtimeAttachmentIdentityRecord{}, errors.New("legacy runtime identity is invalid")
	}
	return record, nil
}
