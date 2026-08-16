package service

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestOpenRecordedRuntimeReleasePreservesAfterRenameWithoutBirthTime(t *testing.T) {
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
	if err == nil || missing || reopened != nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(rename replay) = %#v, %t, %v", reopened, missing, err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, releaseName)); err != nil || !info.IsDir() {
		t.Fatalf("unproven renamed directory = %#v, %v", info, err)
	}
}

func TestOpenRecordedRuntimeReleasePreservesAfterSocketRemovalWithoutBirthTime(t *testing.T) {
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
	if err == nil || missing || reopened != nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(socket replay) = %#v, %t, %v", reopened, missing, err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(releaseRoot); err != nil || !info.IsDir() {
		t.Fatalf("unproven released directory = %#v, %v", info, err)
	}
}

func TestRuntimeAttachmentRecoveryRefusesOnlyNoBirthDirectory(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 9, 0, 0, 0, time.UTC)
	affected := runtimeAttachmentRecoverableTask(t, now, "task-runtime-no-birth-refusal")
	affected.State = domain.TaskCleaned
	affected.StateVersion++
	unaffected := runtimeAttachmentRecoverableTask(t, now, "task-runtime-no-birth-unaffected")
	store := &runtimeAttachmentRecoveryStore{
		tasks: []domain.Task{affected, unaffected},
		preparations: map[string]application.ManagedRunPreparation{
			unaffected.Handle: {
				RequestedWorkspaceRoot: workspace,
				RequestedAttachment: application.PreparedRuntimeAttachment{
					Kind:          application.RuntimeAttachmentUnixSocket,
					SourcePath:    filepath.Join(runtimeRoot, unaffected.Handle, "attachment.sock"),
					RelayIdentity: runtimeTransitionRelayIdentity(),
				},
			},
		},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	releaseName := runtimeAttachmentReleaseName(affected.Handle)
	releaseRoot := filepath.Join(runtimeRoot, releaseName)
	if err := os.Mkdir(releaseRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	affectedSocketPath := filepath.Join(releaseRoot, "attachment.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: affectedSocketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(affectedSocketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	socketIdentity, err := runtimeAttachmentPathIdentity(affectedSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	directoryDescriptor, directoryIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, releaseName)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %t, %v", directoryDescriptor, directoryIdentity, missing, err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor, taskDescriptor: directoryDescriptor,
		taskHandle: affected.Handle, directoryName: releaseName, taskIdentity: directoryIdentity,
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(rootDescriptor, affected.Handle)
	if err != nil {
		t.Fatal(err)
	}
	generation, err = linkRuntimeAttachmentGeneration(pinned, generation, generationID)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleasing, Task: pinned.taskIdentity, Socket: socketIdentity,
		Generation: generation, GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x49),
	}
	record.Task.BirthSec, record.Task.BirthNsec = 0, 0
	if _, err := publishRuntimeAttachmentIdentity(rootDescriptor, affected.Handle, record, nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	servers, err := coordinator.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("recoverRuntimeAttachments(no birth) = %d, %v", len(servers), err)
	}
	if len(store.taskRefusals) != 1 || store.taskRefusals[0].TaskHandle != affected.Handle {
		t.Fatalf("task refusals = %#v", store.taskRefusals)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, releaseName)); err != nil || !info.IsDir() {
		t.Fatalf("affected directory = %#v, %v", info, err)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, unaffected.Handle, "attachment.sock")); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unaffected socket = %#v, %v", info, err)
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
}
