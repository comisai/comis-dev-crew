package service

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestBaseRuntimeRelayIdentityUpgradePreservesUnprovenSocket(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	taskHandle := "task-runtime-relay-base-upgrade"
	taskRoot := filepath.Join(runtimeRoot, taskHandle)
	task := serviceTask()
	task.Handle = taskHandle
	store := &runtimeRelayUpgradeStore{runtimeAttachmentRecoveryStore: runtimeAttachmentRecoveryStore{
		tasks: []domain.Task{task},
	}}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, time.Now().UTC())
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(taskRoot, "attachment.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	seed := runtimeRelaySeedForTest(0x36)
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	store.upgrades = []application.RuntimeRelayIdentityUpgrade{{
		TaskHandle: taskHandle, RelaySeed: seed,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}}
	servers, err := coordinator.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(unproven socket) = %d servers, %v", len(servers), err)
	}
	if len(store.completed) != 0 || len(store.refusals) != 1 ||
		store.refusals[0].TaskHandle != taskHandle {
		t.Fatalf("upgrade outcomes: completed=%#v refusals=%#v", store.completed, store.refusals)
	}
	if info, err := os.Lstat(socketPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unproven base socket was not preserved: %#v, %v", info, err)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, _, found, err := readRuntimeAttachmentIdentityRecord(descriptor, taskHandle)
	if err != nil || found {
		t.Fatalf("unproven base socket identity found = %t, %v", found, err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
}

func TestBaseRuntimeRelayIdentityUpgradePreservesUnprovenEmptyDirectory(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	taskHandle := "task-runtime-relay-base-empty"
	store := &runtimeRelayUpgradeStore{}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, time.Now().UTC())
	if err := os.Mkdir(filepath.Join(runtimeRoot, taskHandle), 0o700); err != nil {
		t.Fatal(err)
	}
	seed := runtimeRelaySeedForTest(0x38)
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	store.upgrades = []application.RuntimeRelayIdentityUpgrade{{
		TaskHandle: taskHandle, RelaySeed: seed,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}}
	if err := coordinator.recoverRuntimeRelayIdentityUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 0 || len(store.refusals) != 1 {
		t.Fatalf("upgrade outcomes: completed=%#v refusals=%#v", store.completed, store.refusals)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, taskHandle)); err != nil || !info.IsDir() {
		t.Fatalf("unproven base directory was not preserved: %#v, %v", info, err)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	_, _, found, err := readRuntimeAttachmentIdentityRecord(descriptor, taskHandle)
	if err != nil || found {
		t.Fatalf("unproven base directory identity found = %t, %v", found, err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
}

func TestBaseRuntimeRelayIdentityUpgradeStartsOnlyWhenPathsAreAbsent(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	taskHandle := "task-runtime-relay-base-absent"
	store := &runtimeRelayUpgradeStore{}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, time.Now().UTC())
	seed := runtimeRelaySeedForTest(0x39)
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle: taskHandle, RelaySeed: seed,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}
	store.upgrades = []application.RuntimeRelayIdentityUpgrade{upgrade}
	if err := coordinator.recoverRuntimeRelayIdentityUpgrades(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.completed) != 1 || store.completed[0] != upgrade || len(store.refusals) != 0 {
		t.Fatalf("completed upgrades = %#v", store.completed)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	record, _, found, err := readRuntimeAttachmentIdentityRecord(descriptor, taskHandle)
	if err != nil || !found || record.Stage != runtimeAttachmentCreatingIntent || record.RelaySeed != seed {
		t.Fatalf("absent base runtime identity = %#v, %t, %v", record, found, err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
}

func TestBaseRuntimeRelayIdentityUpgradePreservesAmbiguousArtifact(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-relay-base-ambiguous"
	taskRoot := filepath.Join(runtimeRoot, taskHandle)
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(taskRoot, "unexpected")
	if err := os.WriteFile(unexpected, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := runtimeRelaySeedForTest(0x37)
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle: taskHandle, RelaySeed: seed,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	if err := upgradeBaseRuntimeAttachmentIdentity(descriptor, upgrade); err == nil {
		t.Fatal("upgradeBaseRuntimeAttachmentIdentity(ambiguous) error = nil")
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(unexpected)
	if err != nil || string(contents) != "preserve" {
		t.Fatalf("ambiguous base artifact = %q, %v", contents, err)
	}
}

func TestIntermediateRuntimeRelayIdentityFormatIsRejected(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	store := &runtimeRelayUpgradeStore{}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, time.Now().UTC())
	taskHandle := "task-runtime-relay-filesystem-upgrade"
	taskRoot := filepath.Join(runtimeRoot, taskHandle)
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(taskRoot, "attachment.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		t.Fatalf("pinTaskRuntimeDirectory() = %#v, %t, %v", pinned, missing, err)
	}
	socketIdentity, err := runtimeAttachmentPathIdentity(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentActive, Task: pinned.taskIdentity, Socket: socketIdentity}
	name, err := runtimeAttachmentIdentityName(taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	encoded := []byte(formatIntermediateRuntimeIdentity(legacy))
	if err := os.WriteFile(filepath.Join(runtimeRoot, name), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	seed := runtimeRelaySeedForTest(0x35)
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	store.upgrades = []application.RuntimeRelayIdentityUpgrade{{
		TaskHandle: taskHandle, RelaySeed: seed,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}}
	if err := coordinator.recoverRuntimeRelayIdentityUpgrades(context.Background()); err == nil {
		t.Fatal("recoverRuntimeRelayIdentityUpgrades(intermediate format) error = nil")
	}
	if len(store.completed) != 0 || len(store.refusals) != 0 {
		t.Fatalf("intermediate format outcomes: completed=%#v refusals=%#v", store.completed, store.refusals)
	}
	stored, err := os.ReadFile(filepath.Join(runtimeRoot, name))
	if err != nil || string(stored) != string(encoded) {
		t.Fatalf("intermediate identity record changed = %q, %v", stored, err)
	}
	_ = pinned.close()
}

func formatIntermediateRuntimeIdentity(record runtimeAttachmentIdentityRecord) string {
	return fmt.Sprintf(
		"%02x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x\n",
		record.Stage,
		record.Task.Device, record.Task.Inode, uint64(record.Task.ChangeSec), uint64(record.Task.ChangeNsec),
		uint64(record.Task.BirthSec), uint64(record.Task.BirthNsec),
		record.Socket.Device, record.Socket.Inode, uint64(record.Socket.ChangeSec), uint64(record.Socket.ChangeNsec),
		uint64(record.Socket.BirthSec), uint64(record.Socket.BirthNsec),
	)
}

type runtimeRelayUpgradeStore struct {
	runtimeAttachmentRecoveryStore
	upgrades  []application.RuntimeRelayIdentityUpgrade
	completed []application.RuntimeRelayIdentityUpgrade
	refusals  []application.RuntimeRelayIdentityRefusal
}

func (store *runtimeRelayUpgradeStore) ListRuntimeRelayIdentityUpgrades(
	context.Context,
) ([]application.RuntimeRelayIdentityUpgrade, error) {
	return append([]application.RuntimeRelayIdentityUpgrade(nil), store.upgrades...), nil
}

func (store *runtimeRelayUpgradeStore) ListRuntimeRelayIdentityRefusals(
	context.Context,
) ([]application.RuntimeRelayIdentityRefusal, error) {
	return append([]application.RuntimeRelayIdentityRefusal(nil), store.refusals...), nil
}

func (store *runtimeRelayUpgradeStore) CompleteRuntimeRelayIdentityUpgrade(
	_ context.Context,
	upgrade application.RuntimeRelayIdentityUpgrade,
) error {
	store.completed = append(store.completed, upgrade)
	store.upgrades = nil
	return nil
}

func (store *runtimeRelayUpgradeStore) RefuseRuntimeRelayIdentityUpgrade(
	_ context.Context,
	upgrade application.RuntimeRelayIdentityUpgrade,
	_ time.Time,
) error {
	store.refusals = append(store.refusals, application.RuntimeRelayIdentityRefusal{
		TaskHandle: upgrade.TaskHandle,
		Reason:     application.RuntimeRelayIdentityUnproven,
	})
	store.upgrades = nil
	return nil
}
