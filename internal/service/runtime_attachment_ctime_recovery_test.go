package service

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenRecordedRuntimeReleaseAcceptsExactGenerationAfterRenameWithoutBirthTime(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-release-rename-replay"
	taskRoot := filepath.Join(runtimeRoot, taskHandle)
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
	socketIdentity, err := runtimeAttachmentPathIdentity(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		t.Fatalf("pinTaskRuntimeDirectory() = %#v, %t, %v", pinned, missing, err)
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(pinned.runtimeRootDescriptor, taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	generation, err = linkRuntimeAttachmentGeneration(pinned, generation, generationID)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent, Task: pinned.taskIdentity, Socket: socketIdentity,
		Generation: generation, GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x47),
	}
	record.Task.BirthSec, record.Task.BirthNsec = 0, 0
	releaseName := runtimeAttachmentReleaseName(taskHandle)
	if err := os.Rename(taskRoot, filepath.Join(runtimeRoot, releaseName)); err != nil {
		t.Fatal(err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(descriptor, taskHandle, record)
	if err != nil || missing || reopened == nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(rename replay) = %#v, %t, %v", reopened, missing, err)
	}
	if err := reopened.close(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRecordedRuntimeReleaseAcceptsExactGenerationAfterSocketRemovalWithoutBirthTime(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-release-socket-replay"
	releaseName := runtimeAttachmentReleaseName(taskHandle)
	releaseRoot := filepath.Join(runtimeRoot, releaseName)
	if err := os.Mkdir(releaseRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, releaseName)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %t, %v", taskDescriptor, taskIdentity, missing, err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, directoryName: releaseName, taskIdentity: taskIdentity,
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(pinned.runtimeRootDescriptor, taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	generation, err = linkRuntimeAttachmentGeneration(pinned, generation, generationID)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleasing, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x48),
	}
	record.Task.BirthSec, record.Task.BirthNsec = 0, 0
	changed := filepath.Join(releaseRoot, "attachment.sock")
	if err := os.WriteFile(changed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(changed); err != nil {
		t.Fatal(err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(descriptor, taskHandle, record)
	if err != nil || missing || reopened == nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(socket replay) = %#v, %t, %v", reopened, missing, err)
	}
	if err := reopened.close(); err != nil {
		t.Fatal(err)
	}
}
