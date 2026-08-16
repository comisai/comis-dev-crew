package service

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPinRuntimeRootRequiresExactBirthAndMountIdentity(t *testing.T) {
	root := shortTempDir(t)
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"), Store: &runtimeAttachmentRecoveryStore{}, Clock: time.Now,
		NewCredential:           func() (string, error) { return "unused-root-credential-0123456789", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	originalBirthSec := coordinator.runtimeRootIdentity.BirthSec
	coordinator.runtimeRootIdentity.BirthSec++
	if descriptor, err := coordinator.pinRuntimeRoot(); err == nil {
		_ = closeRuntimeRootDescriptor(descriptor)
		t.Fatal("pinRuntimeRoot(changed birth identity) error = nil")
	}
	coordinator.runtimeRootIdentity.BirthSec = originalBirthSec
	coordinator.runtimeRootMountID++
	if descriptor, err := coordinator.pinRuntimeRoot(); err == nil {
		_ = closeRuntimeRootDescriptor(descriptor)
		t.Fatal("pinRuntimeRoot(changed mount identity) error = nil")
	}
}

func TestRemovePinnedRuntimeAttachmentClassifiesTaskLocalReplacement(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string, *pinnedTaskRuntimeDirectory, *runtimeAttachmentIdentityRecord)
		want   bool
	}{
		{
			name: "socket",
			mutate: func(t *testing.T, socketPath string, _ *pinnedTaskRuntimeDirectory, _ *runtimeAttachmentIdentityRecord) {
				if err := os.Remove(socketPath); err != nil {
					t.Fatal(err)
				}
				replacement, err := net.ListenUnix("unix", &net.UnixAddr{Name: socketPath, Net: "unix"})
				if err != nil {
					t.Fatal(err)
				}
				replacement.SetUnlinkOnClose(false)
				if err := os.Chmod(socketPath, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = replacement.Close() })
			},
			want: true,
		},
		{
			name: "generation",
			mutate: func(t *testing.T, _ string, pinned *pinnedTaskRuntimeDirectory, record *runtimeAttachmentIdentityRecord) {
				record.Stage = runtimeAttachmentDirectoryBound
				if err := unix.Unlinkat(pinned.taskDescriptor, runtimeAttachmentGenerationLink, 0); err != nil {
					t.Fatal(err)
				}
			},
			want: true,
		},
		{
			name: "descriptor failure",
			mutate: func(t *testing.T, _ string, pinned *pinnedTaskRuntimeDirectory, record *runtimeAttachmentIdentityRecord) {
				record.Stage = runtimeAttachmentDirectoryBound
				if err := unix.Close(pinned.taskDescriptor); err != nil {
					t.Fatal(err)
				}
				pinned.taskDescriptor = -1
			},
			want: false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := shortTempDir(t)
			runtimeRoot := filepath.Join(root, "runtime")
			coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
				RuntimeRoot: runtimeRoot, Store: &runtimeAttachmentRecoveryStore{}, Clock: time.Now,
				NewCredential:           func() (string, error) { return "unused-replacement-credential-012345", nil },
				NewAttentionOperationID: runtimeAttentionOperationID,
			})
			if err != nil {
				t.Fatal(err)
			}
			taskHandle := "task-runtime-cleanup-" + strings.ReplaceAll(test.name, " ", "-")
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
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
			if err != nil || missing {
				t.Fatalf("pinTaskRuntimeDirectory() = %#v, %t, %v", pinned, missing, err)
			}
			record, _, found, err := readRuntimeAttachmentIdentityRecord(pinned.runtimeRootDescriptor, taskHandle)
			if err != nil || !found {
				t.Fatalf("readRuntimeAttachmentIdentityRecord() = %#v, %t, %v", record, found, err)
			}
			test.mutate(t, socketPath, pinned, &record)
			removeErr := removePinnedRuntimeAttachment(pinned, record)
			if got := errors.Is(removeErr, errRuntimeAttachmentOwnershipUnproven); got != test.want {
				t.Fatalf("removePinnedRuntimeAttachment() error = %v, ownership unproven = %t", removeErr, got)
			}
			if pinned.taskDescriptor >= 0 {
				if err := pinned.close(); err != nil {
					t.Fatal(err)
				}
			} else if err := closeRuntimeRootDescriptor(pinned.runtimeRootDescriptor); err != nil {
				t.Fatal(err)
			}
			preservedRoot := taskRoot
			if test.name == "socket" {
				preservedRoot = filepath.Join(runtimeRoot, runtimeAttachmentReleaseName(taskHandle))
			}
			if info, err := os.Lstat(preservedRoot); err != nil || !info.IsDir() {
				t.Fatalf("task-local replacement was not preserved: %#v, %v", info, err)
			}
		})
	}
}

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
