package service

import (
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestLegacyRuntimeRelayIdentityUpgradesToExactDurableSeed(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
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
	if err := os.WriteFile(filepath.Join(runtimeRoot, name), []byte(formatLegacyRuntimeIdentity(legacy)), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := runtimeRelaySeedForTest(0x35)
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	upgrade := application.RuntimeRelayIdentityUpgrade{
		TaskHandle: taskHandle, RelaySeed: seed,
		RelayIdentity: hex.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
	}
	if err := upgradeLegacyRuntimeAttachmentIdentity(pinned.runtimeRootDescriptor, upgrade); err != nil {
		t.Fatal(err)
	}
	stored, _, found, err := readRuntimeAttachmentIdentityRecord(pinned.runtimeRootDescriptor, taskHandle)
	if err != nil || !found || stored.RelaySeed != seed || stored.Task != legacy.Task || stored.Socket != legacy.Socket {
		t.Fatalf("upgraded runtime identity = %#v, %t, %v", stored, found, err)
	}
	_ = pinned.close()
}

func formatLegacyRuntimeIdentity(record runtimeAttachmentIdentityRecord) string {
	return fmt.Sprintf(
		"%02x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x:%016x\n",
		record.Stage,
		record.Task.Device, record.Task.Inode, uint64(record.Task.ChangeSec), uint64(record.Task.ChangeNsec),
		uint64(record.Task.BirthSec), uint64(record.Task.BirthNsec),
		record.Socket.Device, record.Socket.Inode, uint64(record.Socket.ChangeSec), uint64(record.Socket.ChangeNsec),
		uint64(record.Socket.BirthSec), uint64(record.Socket.BirthNsec),
	)
}
