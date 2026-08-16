package service

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

var errRuntimeRelayIdentityUnproven = errors.New("base runtime relay identity artifact is unproven")

func (coordinator *runtimeAttachmentCoordinator) recoverRuntimeRelayIdentityUpgrades(ctx context.Context) error {
	refusals, err := coordinator.store.ListRuntimeRelayIdentityRefusals(ctx)
	if err != nil {
		return errors.New("recover runtime relay identities: durable refusals are unavailable")
	}
	coordinator.runtimeAttachmentRefusals = make(map[string]struct{}, len(refusals))
	for _, refusal := range refusals {
		if refusal.Validate() != nil {
			return errors.New("recover runtime relay identities: durable refusal is invalid")
		}
		coordinator.runtimeAttachmentRefusals[refusal.TaskHandle] = struct{}{}
	}
	upgrades, err := coordinator.store.ListRuntimeRelayIdentityUpgrades(ctx)
	if err != nil {
		return errors.New("recover runtime relay identities: durable upgrades are unavailable")
	}
	for _, upgrade := range upgrades {
		if upgrade.Validate() != nil {
			return errors.New("recover runtime relay identities: durable upgrade is invalid")
		}
		if _, refused := coordinator.runtimeAttachmentRefusals[upgrade.TaskHandle]; refused {
			return errors.New("recover runtime relay identities: durable outcomes conflict")
		}
		descriptor, err := coordinator.pinRuntimeRoot()
		if err != nil {
			return err
		}
		record, _, found, readErr := readRuntimeAttachmentIdentityRecord(descriptor, upgrade.TaskHandle)
		switch {
		case readErr != nil:
			err = errors.Join(errors.New("runtime relay identity authority is ambiguous"), readErr)
		case found:
			prior := record
			record.RelaySeed = upgrade.RelaySeed
			_, err = publishRuntimeAttachmentIdentity(descriptor, upgrade.TaskHandle, record, &prior, nil)
		default:
			err = upgradeBaseRuntimeAttachmentIdentity(descriptor, upgrade)
		}
		closeErr := closeRuntimeRootDescriptor(descriptor)
		if runtimeRelayIdentityUpgradeIsUnproven(err) && closeErr == nil {
			at := coordinator.clock().UTC()
			if at.IsZero() {
				return errors.New("recover runtime relay identities: service time is invalid")
			}
			if err := coordinator.store.RefuseRuntimeRelayIdentityUpgrade(ctx, upgrade, at); err != nil {
				return errors.New("recover runtime relay identities: durable refusal cannot be recorded")
			}
			coordinator.runtimeAttachmentRefusals[upgrade.TaskHandle] = struct{}{}
			continue
		}
		if err != nil || closeErr != nil {
			return errors.Join(
				errors.New("recover runtime relay identities: filesystem authority cannot be upgraded"),
				err,
				closeErr,
			)
		}
		if err := coordinator.store.CompleteRuntimeRelayIdentityUpgrade(ctx, upgrade); err != nil {
			return errors.New("recover runtime relay identities: durable upgrade cannot be completed")
		}
	}
	return nil
}

func runtimeRelayIdentityUpgradeIsUnproven(err error) bool {
	return errors.Is(err, errRuntimeRelayIdentityUnproven) ||
		errors.Is(err, errRuntimeAttachmentOwnershipUnproven) ||
		errors.Is(err, reporter.ErrRuntimePathIdentity) ||
		errors.Is(err, reporter.ErrRuntimePathMissing)
}

func upgradeBaseRuntimeAttachmentIdentity(
	runtimeRootDescriptor int,
	upgrade application.RuntimeRelayIdentityUpgrade,
) error {
	creationAbsent, err := inspectRuntimeAttachmentPathAbsent(
		runtimeRootDescriptor, runtimeAttachmentCreationName(upgrade.TaskHandle),
	)
	if err != nil {
		return err
	}
	if !creationAbsent {
		return runtimeAttachmentOwnershipUnproven("base runtime relay creation path is ambiguous; path preserved")
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(runtimeRootDescriptor, upgrade.TaskHandle)
	if err != nil {
		return err
	}
	if !missing {
		if err := unix.Close(taskDescriptor); err != nil {
			return err
		}
		return errRuntimeRelayIdentityUnproven
	}
	if taskIdentity != (reporter.RuntimeSocketIdentity{}) {
		return errors.New("base runtime relay identity absence is ambiguous")
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(runtimeRootDescriptor, upgrade.TaskHandle)
	if err != nil {
		return err
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation,
		GenerationID: generationID, RelaySeed: upgrade.RelaySeed,
	}
	_, err = publishRuntimeAttachmentIdentity(
		runtimeRootDescriptor, upgrade.TaskHandle, record, nil, nil,
	)
	return err
}
