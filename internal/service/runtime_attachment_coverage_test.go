package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

func TestRuntimeAttachmentErrorClassifiersKeepClosedScope(t *testing.T) {
	infrastructure := []error{
		unix.EBADF, unix.EINTR, unix.EIO, unix.EMFILE,
		unix.ENFILE, unix.ENOMEM, unix.ENOSPC, unix.ETIMEDOUT,
	}
	for _, err := range infrastructure {
		if !runtimeAttachmentOpenFailureIsInfrastructure(err) {
			t.Fatalf("infrastructure error %v was task scoped", err)
		}
		if runtimeAttachmentDirectoryOpenFailureIsUnsafe(-1, "task", err) {
			t.Fatalf("directory infrastructure error %v was unsafe-object scoped", err)
		}
		if runtimeAttachmentRecordOpenFailureIsUnsafe(-1, "record", err) {
			t.Fatalf("record infrastructure error %v was unsafe-object scoped", err)
		}
		if runtimeAttachmentGenerationAnchorOpenFailureIsUnsafe(-1, err) {
			t.Fatalf("generation infrastructure error %v was unsafe-object scoped", err)
		}
	}
	unsafeDirectory := []error{unix.ELOOP, unix.ENOTDIR, unix.EACCES, unix.EPERM, unix.ENXIO, unix.ENODEV, unix.EOPNOTSUPP}
	for _, err := range unsafeDirectory {
		if !runtimeAttachmentDirectoryOpenFailureIsUnsafe(-1, "task", err) {
			t.Fatalf("unsafe directory error %v was not task scoped", err)
		}
	}
	unsafeRecord := []error{unix.ELOOP, unix.EACCES, unix.EPERM, unix.ENXIO, unix.ENODEV, unix.EOPNOTSUPP}
	for _, err := range unsafeRecord {
		if !runtimeAttachmentRecordOpenFailureIsUnsafe(-1, "record", err) {
			t.Fatalf("unsafe record error %v was not task scoped", err)
		}
		if !runtimeAttachmentGenerationAnchorOpenFailureIsUnsafe(-1, err) {
			t.Fatalf("unsafe generation error %v was not task scoped", err)
		}
	}
	if runtimeAttachmentOpenFailureIsInfrastructure(unix.ENOENT) ||
		runtimeAttachmentDirectoryOpenFailureIsUnsafe(-1, "missing", unix.ESTALE) ||
		runtimeAttachmentRecordOpenFailureIsUnsafe(-1, "missing", unix.ESTALE) ||
		runtimeAttachmentGenerationAnchorOpenFailureIsUnsafe(-1, unix.ESTALE) {
		t.Fatal("unknown path errors were assigned unsupported authority")
	}
}

func TestRuntimeAttachmentRecordParserRejectsMalformedAuthority(t *testing.T) {
	identity := reporter.RuntimeSocketIdentity{
		Device: 1, Inode: 2, ChangeSec: 3, ChangeNsec: 4, BirthSec: 5, BirthNsec: 6,
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentActive, Task: identity, Socket: identity, Generation: identity,
		GenerationID: [16]byte{1}, RelaySeed: [32]byte{1},
	}
	encoded := formatRuntimeAttachmentIdentityRecord(record)
	if parsed, err := parseRuntimeAttachmentIdentityRecord(encoded); err != nil || parsed != record {
		t.Fatalf("parseRuntimeAttachmentIdentityRecord(valid) = %#v, %v", parsed, err)
	}

	parts := strings.Split(strings.TrimSuffix(encoded, "\n"), ":")
	mutations := []func([]string) string{
		func(_ []string) string { return "short" },
		func(fields []string) string { return strings.Join(fields[:20], ":") + "\n" },
		func(fields []string) string { fields[0] = "zz"; return strings.Join(fields, ":") + "\n" },
		func(fields []string) string { fields[1] = "1"; return strings.Join(fields, ":") + "\n" },
		func(fields []string) string { fields[2] = "zzzzzzzzzzzzzzzz"; return strings.Join(fields, ":") + "\n" },
		func(fields []string) string { fields[19] = "00"; return strings.Join(fields, ":") + "\n" },
		func(fields []string) string {
			fields[19] = strings.Repeat("z", 32)
			return strings.Join(fields, ":") + "\n"
		},
		func(fields []string) string { fields[20] = "00"; return strings.Join(fields, ":") + "\n" },
		func(fields []string) string {
			fields[20] = strings.Repeat("z", 64)
			return strings.Join(fields, ":") + "\n"
		},
		func(fields []string) string { fields[0] = "ff"; return strings.Join(fields, ":") + "\n" },
		func(fields []string) string {
			fields[19] = strings.Repeat("0", 32)
			return strings.Join(fields, ":") + "\n"
		},
		func(fields []string) string {
			fields[20] = strings.Repeat("0", 64)
			return strings.Join(fields, ":") + "\n"
		},
	}
	for index, mutate := range mutations {
		fields := append([]string(nil), parts...)
		if _, err := parseRuntimeAttachmentIdentityRecord(mutate(fields)); err == nil {
			t.Fatalf("malformed record %d parsed", index)
		}
	}
}

func TestRuntimeAttachmentGenerationLinksExactDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })

	generation, generationID, err := createRuntimeAttachmentGeneration(rootDescriptor, "task-runtime-generation-coverage")
	if err != nil || !generation.Valid() || !runtimeAttachmentGenerationIDValid(generationID) {
		t.Fatalf("createRuntimeAttachmentGeneration() = %#v, %x, %v", generation, generationID, err)
	}
	if !runtimeAttachmentGenerationAvailable(rootDescriptor, generation, generationID) {
		t.Fatal("new generation was unavailable")
	}
	if _, _, _, err := pinRuntimeAttachmentGeneration(-1, reporter.RuntimeSocketIdentity{}, [16]byte{}); !errors.Is(err, errRuntimeAttachmentGenerationDiffers) {
		t.Fatalf("pinRuntimeAttachmentGeneration(invalid) error = %v", err)
	}
	if _, _, err := createRuntimeAttachmentGeneration(-1, ""); err == nil {
		t.Fatal("invalid generation authority succeeded")
	}

	taskHandle := "task-runtime-generation-coverage"
	if err := os.Mkdir(filepath.Join(root, taskHandle), 0o700); err != nil {
		t.Fatal(err)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, taskHandle)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %v, %v", taskDescriptor, taskIdentity, missing, err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: taskHandle, directoryName: taskHandle, taskIdentity: taskIdentity,
	}
	t.Cleanup(func() { _ = unix.Close(taskDescriptor) })
	linked, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID)
	if err != nil || !sameRuntimeAttachmentExactGeneration(linked, generation) {
		t.Fatalf("linkRuntimeAttachmentGeneration() = %#v, %v", linked, err)
	}
	if matches, err := inspectRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil || !matches {
		t.Fatalf("inspectRuntimeAttachmentGeneration() = %v, %v", matches, err)
	}
	if !runtimeAttachmentGenerationMatches(pinned, generation, generationID) {
		t.Fatal("linked generation did not match")
	}
	if _, err := inspectRuntimeAttachmentGeneration(nil, generation, generationID); !errors.Is(err, errRuntimeAttachmentGenerationDiffers) {
		t.Fatalf("inspectRuntimeAttachmentGeneration(nil) error = %v", err)
	}
	if _, err := linkRuntimeAttachmentGeneration(nil, generation, generationID); err == nil {
		t.Fatal("nil pinned generation link succeeded")
	}
}

func TestRuntimeAttachmentDirectoryOpenClassifiesObjects(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })

	if descriptor, _, missing, err := openTaskRuntimeDirectory(rootDescriptor, "missing"); descriptor != -1 || !missing || err != nil {
		t.Fatalf("missing directory = %d, %v, %v", descriptor, missing, err)
	}
	if err := os.Mkdir(filepath.Join(root, "valid"), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, identity, missing, err := openTaskRuntimeDirectory(rootDescriptor, "valid")
	if err != nil || missing || !identity.Valid() {
		t.Fatalf("valid directory = %d, %#v, %v, %v", descriptor, identity, missing, err)
	}
	if err := unix.Close(descriptor); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "regular"), []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := openTaskRuntimeDirectory(rootDescriptor, "regular"); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("regular directory replacement error = %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "valid"), filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := openTaskRuntimeDirectory(rootDescriptor, "symlink"); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("symlink directory replacement error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "unsafe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := openTaskRuntimeDirectory(rootDescriptor, "unsafe"); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("unsafe directory error = %v", err)
	}
}

func TestRuntimeAttachmentEntryWaitsRemainCancelable(t *testing.T) {
	if runtimeAttachmentEntryUnavailable(runtimeAttachmentEntryReady).Error() != "runtime attachment entry is unavailable" ||
		runtimeAttachmentEntryUnavailable(runtimeAttachmentEntryReleasing).Error() != "runtime attachment entry is releasing" {
		t.Fatal("entry state error was not closed")
	}
	entry := &runtimeAttachmentEntry{attachment: application.PreparedRuntimeAttachment{}}
	if attachment, err := runtimeAttachmentRegistrationResult(entry); err != nil || attachment != entry.attachment {
		t.Fatalf("runtimeAttachmentRegistrationResult() = %#v, %v", attachment, err)
	}
	entry.registrationErr = errors.New("registration failed")
	if _, err := runtimeAttachmentRegistrationResult(entry); err == nil {
		t.Fatal("failed registration returned an attachment")
	}
	entry.releaseErr = errors.New("release failed")
	if runtimeAttachmentReleaseResult(entry) == nil {
		t.Fatal("failed release returned nil")
	}

	coordinator := &runtimeAttachmentCoordinator{runDone: make(chan struct{})}
	done := make(chan struct{})
	close(done)
	if err := coordinator.waitRuntimeAttachmentReplay(context.Background(), done, "stopped"); err != nil {
		t.Fatalf("completed replay wait error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.waitRuntimeAttachmentReplay(canceled, make(chan struct{}), "stopped"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replay wait error = %v", err)
	}
	close(coordinator.runDone)
	if err := coordinator.waitRuntimeAttachmentReplay(context.Background(), make(chan struct{}), "stopped"); err == nil || err.Error() != "stopped" {
		t.Fatalf("shutdown replay wait error = %v", err)
	}
}

func TestRuntimeAttachmentFilesystemHelpersPreserveIdentity(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, identity, err := pinRuntimeAttachmentDirectory(root)
	if err != nil || !identity.Valid() {
		t.Fatalf("pinRuntimeAttachmentDirectory() = %d, %#v, %v", descriptor, identity, err)
	}
	if empty, err := runtimeAttachmentDirectoryEmpty(descriptor); err != nil || !empty {
		t.Fatalf("runtimeAttachmentDirectoryEmpty(empty) = %v, %v", empty, err)
	}
	if err := os.WriteFile(filepath.Join(root, "child"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	descriptor, identity, err = pinRuntimeAttachmentDirectory(root)
	if err != nil || !identity.Valid() {
		t.Fatalf("pinRuntimeAttachmentDirectory(nonempty) = %d, %#v, %v", descriptor, identity, err)
	}
	if empty, err := runtimeAttachmentDirectoryEmpty(descriptor); err != nil || empty {
		t.Fatalf("runtimeAttachmentDirectoryEmpty(nonempty) = %v, %v", empty, err)
	}
	if absent, err := inspectRuntimeAttachmentPathAbsent(descriptor, "missing"); err != nil || !absent {
		t.Fatalf("inspectRuntimeAttachmentPathAbsent(missing) = %v, %v", absent, err)
	}
	if absent, err := inspectRuntimeAttachmentPathAbsent(descriptor, "child"); err != nil || absent {
		t.Fatalf("inspectRuntimeAttachmentPathAbsent(existing) = %v, %v", absent, err)
	}
	if _, err := runtimeAttachmentDirectoryEmpty(-1); err == nil {
		t.Fatal("invalid directory descriptor reported contents")
	}
	if _, err := inspectRuntimeAttachmentPathAbsent(-1, "child"); err == nil {
		t.Fatal("invalid descriptor reported path posture")
	}
	if err := closeRuntimeRootDescriptor(descriptor); err != nil {
		t.Fatal(err)
	}
	if err := closeRuntimeRootDescriptor(-1); err == nil {
		t.Fatal("invalid root descriptor closed successfully")
	}

	other := identity
	other.Inode++
	if sameRuntimeAttachmentNode(identity, other) || sameRuntimeAttachmentRoot(identity, other) ||
		sameRuntimeAttachmentStableDirectory(identity, other) || runtimeAttachmentTransitionDirectoryMatches(identity, other) {
		t.Fatal("replacement identity retained runtime authority")
	}
}

func TestRuntimeAttachmentIdentityRecordPublishesExactTransitions(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })

	taskHandle := "task-record-publication-coverage"
	generation, generationID, err := createRuntimeAttachmentGeneration(rootDescriptor, taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	intent := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation, GenerationID: generationID,
		RelaySeed: [32]byte{1},
	}
	firstIdentity, err := publishRuntimeAttachmentIdentity(rootDescriptor, taskHandle, intent, nil, nil)
	if err != nil || !firstIdentity.Valid() {
		t.Fatalf("publishRuntimeAttachmentIdentity(first) = %#v, %v", firstIdentity, err)
	}
	stored, storedIdentity, found, err := readRuntimeAttachmentIdentityRecord(rootDescriptor, taskHandle)
	if err != nil || !found || stored != intent || storedIdentity != firstIdentity {
		t.Fatalf("readRuntimeAttachmentIdentityRecord() = %#v, %#v, %t, %v", stored, storedIdentity, found, err)
	}
	if err := os.Mkdir(filepath.Join(root, taskHandle), 0o700); err != nil {
		t.Fatal(err)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, taskHandle)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %t, %v", taskDescriptor, taskIdentity, missing, err)
	}
	if err := unix.Close(taskDescriptor); err != nil {
		t.Fatal(err)
	}
	bound := intent
	bound.Stage = runtimeAttachmentDirectoryBound
	bound.Task = taskIdentity
	callbackCount := 0
	secondIdentity, err := publishRuntimeAttachmentIdentity(rootDescriptor, taskHandle, bound, &intent, func() { callbackCount++ })
	if err != nil || callbackCount != 1 || !secondIdentity.Valid() {
		t.Fatalf("publishRuntimeAttachmentIdentity(update) = %#v, %v, callbacks=%d", secondIdentity, err, callbackCount)
	}
	if replayIdentity, err := publishRuntimeAttachmentIdentity(rootDescriptor, taskHandle, bound, &bound, nil); err != nil || replayIdentity != secondIdentity {
		t.Fatalf("publishRuntimeAttachmentIdentity(replay) = %#v, %v", replayIdentity, err)
	}
	wrongPrior := intent
	wrongPrior.RelaySeed[0]++
	if _, err := publishRuntimeAttachmentIdentity(rootDescriptor, taskHandle, bound, &wrongPrior, nil); err == nil {
		t.Fatal("publication accepted a different prior record")
	}
	if _, err := prepareRuntimeAttachmentIdentityTemporary(-1, "temporary", bound); err == nil {
		t.Fatal("temporary record accepted an invalid descriptor")
	}
	if _, _, _, err := readRuntimeAttachmentIdentityRecord(rootDescriptor, "invalid handle"); err == nil {
		t.Fatal("invalid task handle acquired a record")
	}

	unsafeHandle := "task-record-unsafe-coverage"
	unsafeName, err := runtimeAttachmentIdentityName(unsafeHandle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, unsafeName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readRuntimeAttachmentIdentityRecord(rootDescriptor, unsafeHandle); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("unsafe identity record error = %v", err)
	}
}

func TestRuntimeAttachmentCleanupRemovesOnlyBoundDirectories(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })
	taskHandle := "task-directory-bound-cleanup"
	if err := os.Mkdir(filepath.Join(root, taskHandle), 0o700); err != nil {
		t.Fatal(err)
	}
	descriptor, identity, missing, err := openTaskRuntimeDirectory(rootDescriptor, taskHandle)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %t, %v", descriptor, identity, missing, err)
	}
	pinnedRootDescriptor, err := unix.Dup(rootDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: pinnedRootDescriptor, taskDescriptor: descriptor,
		taskHandle: taskHandle, directoryName: taskHandle, taskIdentity: identity,
	}
	record := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentDirectoryBound, Task: identity}
	if err := removePinnedTaskRuntimeDirectory(pinned, record); err != nil {
		t.Fatalf("removePinnedTaskRuntimeDirectory() error = %v", err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, taskHandle)); !os.IsNotExist(err) {
		t.Fatalf("bound directory remained: %v", err)
	}

	intent := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentCreatingIntent}
	if err := removeRuntimeAttachmentCreationIntent(rootDescriptor, "task-absent-creation", intent); err != nil {
		t.Fatalf("removeRuntimeAttachmentCreationIntent(absent) error = %v", err)
	}
	canonical := "task-canonical-creation"
	if err := os.Mkdir(filepath.Join(root, canonical), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeAttachmentCreationIntent(rootDescriptor, canonical, intent); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("canonical creation intent error = %v", err)
	}
	creationTask := "task-staged-creation"
	if err := os.Mkdir(filepath.Join(root, runtimeAttachmentCreationName(creationTask)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeAttachmentCreationIntent(rootDescriptor, creationTask, intent); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("staged creation intent error = %v", err)
	}
	missingRecord := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentActive,
		Task:  reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
	}
	if opened, missing, err := openRecordedTaskRuntimeDirectory(rootDescriptor, "task-recorded-missing", missingRecord); err != nil || !missing || opened != nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(missing) = %#v, %t, %v", opened, missing, err)
	}
	if !errors.Is(classifyRuntimeAttachmentCleanupPathError("identity", reporter.ErrRuntimePathIdentity), errRuntimeAttachmentOwnershipUnproven) {
		t.Fatal("identity cleanup failure was not task scoped")
	}
	if errors.Is(classifyRuntimeAttachmentCleanupPathError("resource", unix.EIO), errRuntimeAttachmentOwnershipUnproven) {
		t.Fatal("resource cleanup failure was task scoped")
	}
}
