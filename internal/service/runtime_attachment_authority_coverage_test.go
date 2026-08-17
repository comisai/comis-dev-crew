package service

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRuntimeRelayRecoveryRejectsInconsistentDurableAuthority(t *testing.T) {
	seed := [32]byte{1}
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle: "task-relay-boundary", RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
		RelaySeed: seed,
	}
	now := time.Date(2026, time.August, 17, 4, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		store *runtimeRelayBoundaryStore
		setup func(*testing.T, string, *runtimeAttachmentCoordinator)
	}{
		{name: "refusal read", store: &runtimeRelayBoundaryStore{refusalErr: errors.New("unavailable")}},
		{name: "invalid refusal", store: &runtimeRelayBoundaryStore{refusals: []application.RuntimeRelayIdentityRefusal{{}}}},
		{name: "upgrade read", store: &runtimeRelayBoundaryStore{upgradeErr: errors.New("unavailable")}},
		{name: "invalid upgrade", store: &runtimeRelayBoundaryStore{upgrades: []application.RuntimeRelayIdentityUpgrade{{}}}},
		{name: "conflicting outcomes", store: &runtimeRelayBoundaryStore{
			refusals: []application.RuntimeRelayIdentityRefusal{{TaskHandle: upgrade.TaskHandle, Reason: application.RuntimeRelayIdentityUnproven}},
			upgrades: []application.RuntimeRelayIdentityUpgrade{upgrade},
		}},
		{name: "zero refusal time", store: &runtimeRelayBoundaryStore{upgrades: []application.RuntimeRelayIdentityUpgrade{upgrade}}, setup: func(t *testing.T, root string, coordinator *runtimeAttachmentCoordinator) {
			if err := os.Mkdir(filepath.Join(root, runtimeAttachmentCreationName(upgrade.TaskHandle)), 0o700); err != nil {
				t.Fatal(err)
			}
			coordinator.clock = func() time.Time { return time.Time{} }
		}},
		{name: "refusal write", store: &runtimeRelayBoundaryStore{
			upgrades: []application.RuntimeRelayIdentityUpgrade{upgrade}, refuseErr: errors.New("unavailable"),
		}, setup: func(t *testing.T, root string, _ *runtimeAttachmentCoordinator) {
			if err := os.Mkdir(filepath.Join(root, runtimeAttachmentCreationName(upgrade.TaskHandle)), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "completion write", store: &runtimeRelayBoundaryStore{
			upgrades: []application.RuntimeRelayIdentityUpgrade{upgrade}, completeErr: errors.New("unavailable"),
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(shortTempDir(t), "runtime")
			coordinator := runtimeTransitionCoordinator(t, root, test.store, now)
			if test.setup != nil {
				test.setup(t, root, coordinator)
			}
			if err := coordinator.recoverRuntimeRelayIdentityUpgrades(context.Background()); err == nil {
				t.Fatal("inconsistent relay recovery succeeded")
			}
		})
	}
}

func TestRuntimeAttachmentReleaseStateMachineRejectsClosedAuthority(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	stopped := make(chan struct{})
	close(stopped)
	coordinator := &runtimeAttachmentCoordinator{
		recoveryReady: ready, runDone: stopped, releases: make(chan runtimeAttachmentRelease),
		entries:                   make(map[string]*runtimeAttachmentEntry),
		runtimeAttachmentRefusals: map[string]struct{}{"task-release-refused": {}},
	}
	//lint:ignore SA1012 The boundary test proves release rejects nil request authority.
	if err := coordinator.ReleaseRuntimeAttachment(nil, "task-release-boundary"); err == nil {
		t.Fatal("release accepted a nil context")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.ReleaseRuntimeAttachment(canceled, "task-release-boundary"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReleaseRuntimeAttachment(canceled) error = %v", err)
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), "invalid handle"); err == nil {
		t.Fatal("release accepted an invalid task handle")
	}
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), "task-release-refused"); err == nil {
		t.Fatal("release accepted refused filesystem authority")
	}
	invalidEntry := &runtimeAttachmentEntry{state: runtimeAttachmentEntryState(255)}
	coordinator.entries["task-release-invalid-state"] = invalidEntry
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), "task-release-invalid-state"); err == nil {
		t.Fatal("release accepted an invalid entry state")
	}
	if err := coordinator.releaseRuntimeServer(nil); err == nil {
		t.Fatal("releaseRuntimeServer accepted a stopped coordinator")
	}
	registration := &runtimeAttachmentEntry{state: runtimeAttachmentEntryPending, registrationDone: make(chan struct{})}
	coordinator.entries["task-registration-failed"] = registration
	registrationErr := errors.New("registration failed")
	coordinator.completeRuntimeAttachmentRegistration("task-registration-failed", registration, registrationErr)
	if registration.registrationErr != registrationErr || registration.state != runtimeAttachmentEntryReady ||
		coordinator.entries["task-registration-failed"] != nil {
		t.Fatal("failed registration did not close exact entry")
	}
	select {
	case <-registration.registrationDone:
	default:
		t.Fatal("failed registration did not signal completion")
	}
	release := &runtimeAttachmentEntry{state: runtimeAttachmentEntryReleasing, releaseDone: make(chan struct{})}
	coordinator.entries["task-release-complete"] = release
	coordinator.completeRuntimeAttachmentRelease("task-release-complete", release, nil)
	if coordinator.entries["task-release-complete"] != nil {
		t.Fatal("completed release retained its entry")
	}
	select {
	case <-release.releaseDone:
	default:
		t.Fatal("completed release did not signal completion")
	}
	if err := closeRuntimeServers(nil); err != nil {
		t.Fatalf("closeRuntimeServers(nil) error = %v", err)
	}
	if _, err := isolatePinnedRuntimeAttachmentRelease(nil, runtimeAttachmentIdentityRecord{}); err == nil {
		t.Fatal("release isolation accepted nil authority")
	}
	pinned := &pinnedTaskRuntimeDirectory{taskHandle: "task-release-location", directoryName: "unexpected"}
	if _, err := isolatePinnedRuntimeAttachmentRelease(pinned, runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentReleaseIntent}); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("ambiguous release location error = %v", err)
	}
	releaseName := runtimeAttachmentReleaseName(pinned.taskHandle)
	pinned.directoryName = releaseName
	record := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentReleasing}
	if isolated, err := isolatePinnedRuntimeAttachmentRelease(pinned, record); err != nil || isolated != record {
		t.Fatalf("isolatePinnedRuntimeAttachmentRelease(replay) = %#v, %v", isolated, err)
	}
}

func TestServiceOptionalComponentsRequireCompleteAuthority(t *testing.T) {
	root := shortTempDir(t)
	if err := Run(context.Background(), Config{
		DatabasePath: filepath.Join(root, "cleanup.db"), SocketPath: filepath.Join(root, "cleanup.sock"),
		cleanupRemover: boundaryDeliveredWorkspaceRemover{},
	}); err == nil {
		t.Fatal("Run accepted incomplete cleanup authority")
	}
	if err := Run(context.Background(), Config{
		DatabasePath: filepath.Join(root, "fixture.db"), SocketPath: filepath.Join(root, "fixture.sock"),
		FixtureComposition: &FixtureComposition{Decision: "invalid"},
	}); err == nil {
		t.Fatal("Run accepted incomplete fixture authority")
	}
}

type boundaryDeliveredWorkspaceRemover struct{}

func (boundaryDeliveredWorkspaceRemover) RemoveDeliveredWorkspace(
	context.Context, application.DeliveredWorkspaceRemoval,
) error {
	return nil
}

type runtimeRelayBoundaryStore struct {
	runtimeAttachmentRecoveryStore
	refusals    []application.RuntimeRelayIdentityRefusal
	upgrades    []application.RuntimeRelayIdentityUpgrade
	refusalErr  error
	upgradeErr  error
	refuseErr   error
	completeErr error
}

func (store *runtimeRelayBoundaryStore) ListRuntimeRelayIdentityRefusals(context.Context) ([]application.RuntimeRelayIdentityRefusal, error) {
	return append([]application.RuntimeRelayIdentityRefusal(nil), store.refusals...), store.refusalErr
}

func (store *runtimeRelayBoundaryStore) ListRuntimeRelayIdentityUpgrades(context.Context) ([]application.RuntimeRelayIdentityUpgrade, error) {
	return append([]application.RuntimeRelayIdentityUpgrade(nil), store.upgrades...), store.upgradeErr
}

func (store *runtimeRelayBoundaryStore) RefuseRuntimeRelayIdentityUpgrade(
	context.Context, application.RuntimeRelayIdentityUpgrade, time.Time,
) error {
	return store.refuseErr
}

func (store *runtimeRelayBoundaryStore) CompleteRuntimeRelayIdentityUpgrade(
	context.Context, application.RuntimeRelayIdentityUpgrade,
) error {
	return store.completeErr
}
