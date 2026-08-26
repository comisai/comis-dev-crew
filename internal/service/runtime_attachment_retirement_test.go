package service

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

func TestRetireUncommittedRuntimeAttachmentSocketRefusesUnsafeNode(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-unsafe-uncommitted")
	path := filepath.Join(runtimeRoot, pinned.taskHandle, "attachment.sock")
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := retireUncommittedRuntimeAttachmentSocket(pinned); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("retireUncommittedRuntimeAttachmentSocket(regular file) error = %v", err)
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "preserve" {
		t.Fatalf("unsafe uncommitted node = %q, %v", contents, err)
	}
}

func TestRetireUncommittedRuntimeAttachmentSocketRefusesSharedNamespace(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-shared-uncommitted")
	taskRoot := filepath.Join(runtimeRoot, pinned.taskHandle)
	path := filepath.Join(taskRoot, "attachment.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(path)
	})
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(taskRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(taskRoot, 0o700) })
	if err := retireUncommittedRuntimeAttachmentSocket(pinned); err == nil {
		t.Fatal("retireUncommittedRuntimeAttachmentSocket accepted a shared namespace")
	}
	if info, err := os.Lstat(path); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("shared-namespace socket = %#v, %v", info, err)
	}
}

func TestRetirePinnedRuntimeAttachmentGenerationLinkRefusesMissingAnchor(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-missing-generation")
	path := filepath.Join(runtimeRoot, pinned.taskHandle, runtimeAttachmentGenerationLink)
	if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Generation:   reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		GenerationID: [16]byte{1},
	}
	if err := retirePinnedRuntimeAttachmentGenerationLink(pinned, record); !errors.Is(
		err, errRuntimeAttachmentGenerationDiffers,
	) {
		t.Fatalf("retirePinnedRuntimeAttachmentGenerationLink(missing anchor) error = %v", err)
	}
	if contents, err := os.ReadFile(path); err != nil || string(contents) != "preserve" {
		t.Fatalf("unbound generation link = %q, %v", contents, err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryRefusesMissingGenerationAnchor(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-missing-generation")
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating,
		Task:  pinned.taskIdentity,
		Generation: reporter.RuntimeSocketIdentity{
			Device: 1, Inode: 2, ChangeSec: 3,
		},
		GenerationID: [16]byte{1},
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(missing generation) error = %v", err)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, pinned.taskHandle)); err != nil || !info.IsDir() {
		t.Fatalf("missing-generation task directory = %#v, %v", info, err)
	}
}

func TestRemovePinnedDirectoryBoundRuntimeDirectoryPropagatesSharedGenerationRefusal(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-bound-shared-generation")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(runtimeRoot, pinned.taskHandle)
	if err := os.Chmod(taskRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(taskRoot, 0o700) })
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); err == nil {
		t.Fatal("removePinnedTaskRuntimeDirectory accepted a shared bound directory")
	}
	if info, err := os.Lstat(taskRoot); err != nil || !info.IsDir() {
		t.Fatalf("shared bound directory = %#v, %v", info, err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryPropagatesSharedGenerationRefusal(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-shared-generation")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(runtimeRoot, pinned.taskHandle)
	if err := os.Chmod(taskRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(taskRoot, 0o700) })
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); err == nil {
		t.Fatal("removePinnedTaskRuntimeDirectory accepted a shared creating directory")
	}
	if info, err := os.Lstat(taskRoot); err != nil || !info.IsDir() {
		t.Fatalf("shared creating directory = %#v, %v", info, err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryPreservesUnsafeUncommittedNode(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-unsafe-node")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	unsafePath := filepath.Join(runtimeRoot, pinned.taskHandle, "attachment.sock")
	if err := os.WriteFile(unsafePath, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(unsafe node) error = %v", err)
	}
	if contents, err := os.ReadFile(unsafePath); err != nil || string(contents) != "preserve" {
		t.Fatalf("unsafe creating node = %q, %v", contents, err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryRefusesUnsafeGenerationAnchor(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-unsafe-generation")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(
		runtimeRoot, runtimeAttachmentGenerationName(generationID), runtimeAttachmentGenerationLink,
	)
	if err := os.Remove(anchor); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", anchor); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(unsafe generation) error = %v", err)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, pinned.taskHandle)); err != nil || !info.IsDir() {
		t.Fatalf("unsafe-generation task directory = %#v, %v", info, err)
	}
}

func TestOpenRecordedRuntimeDirectoryRefusesUnsafeGenerationAnchor(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-open-unsafe-generation")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	anchor := filepath.Join(
		runtimeRoot, runtimeAttachmentGenerationName(generationID), runtimeAttachmentGenerationLink,
	)
	if err := os.Remove(anchor); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", anchor); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID,
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(
		pinned.runtimeRootDescriptor, pinned.taskHandle, record,
	)
	if err == nil || missing || reopened != nil {
		if reopened != nil {
			_ = reopened.close()
		}
		t.Fatalf("openRecordedTaskRuntimeDirectory(unsafe generation) = %#v, %t, %v", reopened, missing, err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryRefusesUnboundGenerationLink(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-unbound-link")
	link := filepath.Join(runtimeRoot, pinned.taskHandle, runtimeAttachmentGenerationLink)
	if err := os.WriteFile(link, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation:   reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		GenerationID: [16]byte{1},
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(unbound link) error = %v", err)
	}
	if contents, err := os.ReadFile(link); err != nil || string(contents) != "preserve" {
		t.Fatalf("unbound creating link = %q, %v", contents, err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryPreservesUnexpectedChild(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-unexpected")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(runtimeRoot, pinned.taskHandle, "unexpected")
	if err := os.WriteFile(unexpected, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(unexpected child) error = %v", err)
	}
	if !runtimeRetirementFileHasContents(t, runtimeRoot, "unexpected", "preserve") {
		t.Fatal("unexpected creating child was not preserved")
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryReportsRecordDriftAfterRetirement(t *testing.T) {
	_, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-record-drift")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Socket:     reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(record drift) error = %v", err)
	}
}

func TestRemovePinnedCreatingRuntimeDirectoryRefusesMismatchedGenerationAfterSocketRetirement(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-creating-mismatched-link")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherAnchor := filepath.Join(runtimeRoot, "creating-other-generation-anchor")
	if err := os.WriteFile(otherAnchor, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(
		otherAnchor, filepath.Join(runtimeRoot, pinned.taskHandle, runtimeAttachmentGenerationLink),
	); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Socket:     reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		Generation: generation, GenerationID: generationID,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(mismatched link) error = %v", err)
	}
}

func TestOpenRecordedRuntimeDirectoryReconcilesExactStrandedEmptyTarget(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-stranded-empty")
	unexpected := filepath.Join(runtimeRoot, pinned.taskHandle, "unexpected")
	if err := os.WriteFile(unexpected, []byte("remove before replay"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := pinned.taskIdentity
	err := reporter.QuarantineRuntimePath(
		pinned.runtimeRootDescriptor, pinned.directoryName, expected, reporter.RuntimePathDirectory, 0o700,
	)
	if !errors.Is(err, reporter.ErrRuntimePathIdentity) {
		t.Fatalf("QuarantineRuntimePath(stranded directory) error = %v", err)
	}
	if err := unix.Unlinkat(pinned.taskDescriptor, "unexpected", 0); err != nil {
		t.Fatal(err)
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
		runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentActive, Task: expected},
	)
	if err != nil || !missing || reopened != nil {
		if reopened != nil {
			_ = reopened.close()
		}
		t.Fatalf("openRecordedTaskRuntimeDirectory(stranded empty) = %#v, %t, %v", reopened, missing, err)
	}
}

func TestRetirePinnedRuntimeAttachmentGenerationLinkPreservesMismatchedHardLink(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-mismatched-generation")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	otherAnchor := filepath.Join(runtimeRoot, "other-generation-anchor")
	if err := os.WriteFile(otherAnchor, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(runtimeRoot, pinned.taskHandle, runtimeAttachmentGenerationLink)
	if err := os.Link(otherAnchor, link); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{Generation: generation, GenerationID: generationID}
	if err := retirePinnedRuntimeAttachmentGenerationLink(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("retirePinnedRuntimeAttachmentGenerationLink(mismatched link) error = %v", err)
	}
	if contents, err := os.ReadFile(link); err != nil || string(contents) != "preserve" {
		t.Fatalf("mismatched generation link = %q, %v", contents, err)
	}
}

func TestRetirePinnedRuntimeAttachmentGenerationLinkRefusesSharedNamespace(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-shared-generation")
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	taskRoot := filepath.Join(runtimeRoot, pinned.taskHandle)
	if err := os.Chmod(taskRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(taskRoot, 0o700) })
	record := runtimeAttachmentIdentityRecord{Generation: generation, GenerationID: generationID}
	if err := retirePinnedRuntimeAttachmentGenerationLink(pinned, record); err == nil {
		t.Fatal("retirePinnedRuntimeAttachmentGenerationLink accepted a shared namespace")
	}
	if _, err := os.Lstat(filepath.Join(taskRoot, runtimeAttachmentGenerationLink)); err != nil {
		t.Fatalf("shared-namespace generation link was not preserved: %v", err)
	}
}

func TestRuntimeAttachmentRetirementReportsClosedDirectoryDescriptor(t *testing.T) {
	t.Run("uncommitted socket", func(t *testing.T) {
		_, pinned := runtimeRetirementPinnedTask(t, "task-runtime-closed-uncommitted")
		if err := pinned.close(); err != nil {
			t.Fatal(err)
		}
		pinned.taskDescriptor = -1
		pinned.runtimeRootDescriptor = -1
		if err := retireUncommittedRuntimeAttachmentSocket(pinned); err == nil {
			t.Fatal("retireUncommittedRuntimeAttachmentSocket accepted a closed descriptor")
		}
	})

	t.Run("generation link", func(t *testing.T) {
		_, pinned := runtimeRetirementPinnedTask(t, "task-runtime-closed-generation")
		if err := pinned.close(); err != nil {
			t.Fatal(err)
		}
		pinned.taskDescriptor = -1
		pinned.runtimeRootDescriptor = -1
		if err := retirePinnedRuntimeAttachmentGenerationLink(
			pinned, runtimeAttachmentIdentityRecord{},
		); err == nil {
			t.Fatal("retirePinnedRuntimeAttachmentGenerationLink accepted a closed descriptor")
		}
	})
}

func TestStagePinnedRuntimeAttachmentDirectoryRefusesIdentityAndRecordDrift(t *testing.T) {
	t.Run("directory identity", func(t *testing.T) {
		_, pinned := runtimeRetirementPinnedTask(t, "task-runtime-stage-identity")
		pinned.taskIdentity.Inode++
		if _, err := stagePinnedRuntimeAttachmentDirectory(
			pinned, runtimeAttachmentIdentityRecord{},
		); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
			t.Fatalf("stagePinnedRuntimeAttachmentDirectory(identity drift) error = %v", err)
		}
	})

	t.Run("record identity", func(t *testing.T) {
		_, pinned := runtimeRetirementPinnedTask(t, "task-runtime-stage-record")
		record := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentReleasing, Task: pinned.taskIdentity}
		if _, err := stagePinnedRuntimeAttachmentDirectory(pinned, record); !errors.Is(
			err, errRuntimeAttachmentOwnershipUnproven,
		) {
			t.Fatalf("stagePinnedRuntimeAttachmentDirectory(record drift) error = %v", err)
		}
	})
}

func TestRemovePinnedReleasingRuntimeDirectoryPreservesUnexpectedChild(t *testing.T) {
	runtimeRoot, pinned := runtimeRetirementPinnedTask(t, "task-runtime-release-unexpected")
	releaseName := runtimeAttachmentReleaseName(pinned.taskHandle)
	if err := os.Rename(
		filepath.Join(runtimeRoot, pinned.taskHandle), filepath.Join(runtimeRoot, releaseName),
	); err != nil {
		t.Fatal(err)
	}
	pinned.directoryName = releaseName
	current, err := runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	pinned.taskIdentity = current
	generation, generationID, err := createRuntimeAttachmentGeneration(
		pinned.runtimeRootDescriptor, pinned.taskHandle,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	unexpected := filepath.Join(runtimeRoot, releaseName, "unexpected")
	if err := os.WriteFile(unexpected, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleasing, Task: current,
		Socket:     reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		Generation: generation, GenerationID: generationID,
		RelaySeed: runtimeRelaySeedForTest(0x5a),
	}
	if _, err := publishRuntimeAttachmentIdentity(
		pinned.runtimeRootDescriptor, pinned.taskHandle, record, nil, nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); !errors.Is(
		err, errRuntimeAttachmentOwnershipUnproven,
	) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(unexpected child) error = %v", err)
	}
	if !runtimeRetirementFileHasContents(t, runtimeRoot, "unexpected", "preserve") {
		t.Fatal("unexpected release child was not preserved")
	}
}

func runtimeRetirementFileHasContents(t *testing.T, root, name, expected string) bool {
	t.Helper()
	var matched bool
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() != name {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		matched = string(contents) == expected
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return matched
}

func runtimeRetirementPinnedTask(t *testing.T, taskHandle string) (string, *pinnedTaskRuntimeDirectory) {
	t.Helper()
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(
		t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC(),
	)
	if err := os.Mkdir(filepath.Join(runtimeRoot, taskHandle), 0o700); err != nil {
		t.Fatal(err)
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		t.Fatalf("pinTaskRuntimeDirectory() = %#v, %t, %v", pinned, missing, err)
	}
	t.Cleanup(func() { _ = pinned.close() })
	return runtimeRoot, pinned
}
