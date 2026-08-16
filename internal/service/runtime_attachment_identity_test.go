package service

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPersistRuntimeAttachmentIdentityPreservesRacedHardLink(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: runtimeRoot, Store: &runtimeAttachmentRecoveryStore{}, Clock: time.Now,
		NewCredential:           func() (string, error) { return "unused-record-credential-0123456789", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	taskHandle := "task-runtime-record-publication"
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
	socketIdentity, err := runtimeAttachmentPathIdentity(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.persistRuntimeAttachmentIdentity(taskHandle, socketIdentity); err != nil {
		t.Fatal(err)
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		t.Fatalf("pinTaskRuntimeDirectory() = %#v, missing=%t, %v", pinned, missing, err)
	}
	defer pinned.close()
	record, _, found, err := readRuntimeAttachmentIdentityRecord(pinned.runtimeRootDescriptor, taskHandle)
	if err != nil || !found {
		t.Fatalf("readRuntimeAttachmentIdentityRecord() = %#v, %t, %v", record, found, err)
	}
	sentinelPath := filepath.Join(root, "sentinel")
	sentinelContents := []byte("preserve hard-linked content")
	if err := os.WriteFile(sentinelPath, sentinelContents, 0o600); err != nil {
		t.Fatal(err)
	}
	name, err := runtimeAttachmentIdentityName(taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(runtimeRoot, name)
	publishErr := persistPinnedRuntimeAttachmentIdentity(pinned, record, func() {
		if err := os.Remove(recordPath); err != nil {
			t.Error(err)
			return
		}
		if err := os.Link(sentinelPath, recordPath); err != nil {
			t.Error(err)
		}
	})
	if publishErr == nil {
		t.Fatal("persistPinnedRuntimeAttachmentIdentity(raced hard link) error = nil")
	}
	contents, err := os.ReadFile(sentinelPath)
	if err != nil || string(contents) != string(sentinelContents) {
		t.Fatalf("hard-linked contents = %q, %v", contents, err)
	}
	recordInfo, recordErr := os.Lstat(recordPath)
	sentinelInfo, sentinelErr := os.Lstat(sentinelPath)
	if recordErr != nil || sentinelErr != nil || !os.SameFile(recordInfo, sentinelInfo) {
		t.Fatalf("raced hard link was not preserved: %#v, %#v, %v, %v", recordInfo, sentinelInfo, recordErr, sentinelErr)
	}
}

func TestPersistRuntimeAttachmentIdentityIgnoresIncompletePriorTemporary(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: runtimeRoot, Store: &runtimeAttachmentRecoveryStore{}, Clock: time.Now,
		NewCredential:           func() (string, error) { return "unused-temporary-credential-0123456789", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntimeRootDescriptor(descriptor)
	taskHandle := "task-runtime-record-incomplete-temporary"
	generation, generationID, err := createRuntimeAttachmentGeneration(descriptor, taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation,
		GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x41),
	}
	staleName, err := runtimeAttachmentIdentityTemporaryName(taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	stalePath := filepath.Join(runtimeRoot, staleName)
	if err := os.WriteFile(stalePath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := publishRuntimeAttachmentIdentity(descriptor, taskHandle, record, nil, nil); err != nil {
		t.Fatalf("publishRuntimeAttachmentIdentity(incomplete prior temporary) error = %v", err)
	}
	persisted, _, found, err := readRuntimeAttachmentIdentityRecord(descriptor, taskHandle)
	if err != nil || !found || persisted != record {
		t.Fatalf("readRuntimeAttachmentIdentityRecord() = %#v, %t, %v", persisted, found, err)
	}
	contents, err := os.ReadFile(stalePath)
	if err != nil || string(contents) != "partial" {
		t.Fatalf("incomplete prior temporary was altered: %q, %v", contents, err)
	}
}

func runtimeRelaySeedForTest(value byte) [32]byte {
	var seed [32]byte
	for index := range seed {
		seed[index] = value
	}
	return seed
}
