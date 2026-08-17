package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"golang.org/x/sys/unix"
)

func TestRuntimeAttachmentFilesystemEdgesRejectUnstableAuthority(t *testing.T) {
	root, err := filepath.EvalSymlinks(shortTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	validTask := filepath.Join(root, "task-runtime-path-valid")
	if err := ensureTaskRuntimeDirectory(validTask); err != nil {
		t.Fatalf("ensureTaskRuntimeDirectory(valid) error = %v", err)
	}
	publicTask := filepath.Join(root, "task-runtime-path-public")
	if err := os.Mkdir(publicTask, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensureTaskRuntimeDirectory(publicTask); err == nil {
		t.Fatal("ensureTaskRuntimeDirectory(public) error = nil")
	}
	symlinkTask := filepath.Join(root, "task-runtime-path-symlink")
	if err := os.Symlink(validTask, symlinkTask); err != nil {
		t.Fatal(err)
	}
	if err := ensureTaskRuntimeDirectory(symlinkTask); err == nil {
		t.Fatal("ensureTaskRuntimeDirectory(symlink) error = nil")
	}
	missing := filepath.Join(root, "missing", "runtime")
	if descriptor, identity, err := pinRuntimeAttachmentDirectory(missing); err == nil || descriptor != -1 || identity.Valid() {
		t.Fatalf("pinRuntimeAttachmentDirectory(missing) = %d, %#v, %v", descriptor, identity, err)
	}
	if _, _, err := runtimeAttachmentDirectoryIdentity(missing); err == nil {
		t.Fatal("runtimeAttachmentDirectoryIdentity(missing) error = nil")
	}
	if descriptor, _, absent, err := openTaskRuntimeDirectory(-1, "task-runtime-invalid-root"); err == nil || absent || descriptor != -1 {
		t.Fatalf("openTaskRuntimeDirectory(invalid root) = %d, %t, %v", descriptor, absent, err)
	}
	if sec, nsec := runtimeAttachmentDescriptorBirthTime(-1); sec != 0 || nsec != 0 {
		t.Fatalf("invalid descriptor birth time = %d, %d", sec, nsec)
	}
	if sec, nsec := runtimeAttachmentPathBirthTime(missing); sec != 0 || nsec != 0 {
		t.Fatalf("missing path birth time = %d, %d", sec, nsec)
	}
	if sec, nsec := runtimeAttachmentChildBirthTime(-1, "missing"); sec != 0 || nsec != 0 {
		t.Fatalf("invalid child birth time = %d, %d", sec, nsec)
	}
	if mountID, err := runtimeAttachmentDescriptorMountID(-1); err == nil || mountID != 0 {
		t.Fatalf("runtimeAttachmentDescriptorMountID(invalid) = %d, %v", mountID, err)
	}
}

func TestRuntimeAttachmentGenerationRejectsUnavailableDestinations(t *testing.T) {
	root := shortTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })
	closedDescriptor, err := unix.Dup(rootDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(closedDescriptor); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createRuntimeAttachmentGeneration(closedDescriptor, "task-generation-closed-root"); err == nil {
		t.Fatal("generation accepted a closed root descriptor")
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(rootDescriptor, "task-generation-invalid-link")
	if err != nil {
		t.Fatal(err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor,
		taskDescriptor:        -1,
		taskHandle:            "task-generation-invalid-link",
		directoryName:         "task-generation-invalid-link",
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err == nil {
		t.Fatal("generation linked through an invalid task descriptor")
	}
	missingGenerationID := [16]byte{0x71}
	if _, _, _, err := pinRuntimeAttachmentGeneration(
		rootDescriptor,
		reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		missingGenerationID,
	); !errors.Is(err, errRuntimeAttachmentGenerationDiffers) {
		t.Fatalf("pinRuntimeAttachmentGeneration(missing) error = %v", err)
	}
	symlinkGenerationID := [16]byte{0x72}
	symlinkGenerationName := runtimeAttachmentGenerationName(symlinkGenerationID)
	if err := os.Mkdir(filepath.Join(root, symlinkGenerationName), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkGeneration, err := runtimeAttachmentPathIdentity(filepath.Join(root, symlinkGenerationName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target", filepath.Join(root, symlinkGenerationName, runtimeAttachmentGenerationLink)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := pinRuntimeAttachmentGeneration(rootDescriptor, symlinkGeneration, symlinkGenerationID); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("pinRuntimeAttachmentGeneration(symlink anchor) error = %v", err)
	}
}

func TestRuntimeAttachmentCleanupRejectsAmbiguousAndChangedDirectories(t *testing.T) {
	root := shortTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })
	if err := removeRuntimeAttachmentCreationIntent(-1, "task-creation-invalid-root", runtimeAttachmentIdentityRecord{}); err == nil {
		t.Fatal("creation cleanup accepted an invalid root descriptor")
	}

	taskHandle := "task-cleanup-ambiguous-location"
	canonical := filepath.Join(root, taskHandle)
	staged := filepath.Join(root, runtimeAttachmentCreationName(taskHandle))
	if err := os.Mkdir(canonical, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalDescriptor, canonicalIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, taskHandle)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory(canonical) = %d, %#v, %t, %v", canonicalDescriptor, canonicalIdentity, missing, err)
	}
	if err := unix.Close(canonicalDescriptor); err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{Stage: runtimeAttachmentDirectoryBound, Task: canonicalIdentity}
	if opened, missing, err := openRecordedTaskRuntimeDirectory(rootDescriptor, taskHandle, record); err == nil || missing || opened != nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(ambiguous) = %#v, %t, %v", opened, missing, err)
	}

	changedTask := "task-cleanup-changed-directory"
	changedPath := filepath.Join(root, changedTask)
	if err := os.Mkdir(changedPath, 0o700); err != nil {
		t.Fatal(err)
	}
	changedDescriptor, changedIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, changedTask)
	if err != nil || missing {
		t.Fatal(err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor,
		taskDescriptor:        changedDescriptor,
		taskHandle:            changedTask,
		directoryName:         changedTask,
		taskIdentity:          changedIdentity,
	}
	if err := os.WriteFile(filepath.Join(changedPath, "unexpected"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound,
		Task:  changedIdentity,
	}); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(nonempty) error = %v", err)
	}
	if err := unix.Close(changedDescriptor); err != nil {
		t.Fatal(err)
	}

	mismatchTask := "task-cleanup-identity-mismatch"
	if err := os.Mkdir(filepath.Join(root, mismatchTask), 0o700); err != nil {
		t.Fatal(err)
	}
	mismatchDescriptor, mismatchIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, mismatchTask)
	if err != nil || missing {
		t.Fatal(err)
	}
	mismatchPinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor,
		taskDescriptor:        mismatchDescriptor,
		taskHandle:            mismatchTask,
		directoryName:         mismatchTask,
		taskIdentity:          mismatchIdentity,
	}
	differentIdentity := mismatchIdentity
	differentIdentity.Inode++
	if err := removePinnedTaskRuntimeDirectory(mismatchPinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound,
		Task:  differentIdentity,
	}); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("removePinnedTaskRuntimeDirectory(identity mismatch) error = %v", err)
	}
	if _, err := stagePinnedRuntimeAttachmentDirectory(mismatchPinned, runtimeAttachmentIdentityRecord{}); err == nil {
		t.Fatal("stagePinnedRuntimeAttachmentDirectory accepted missing prior record")
	}
	if _, err := inspectRuntimeAttachmentSocket(-1, reporter.RuntimeSocketIdentity{}); err == nil {
		t.Fatal("inspectRuntimeAttachmentSocket accepted an invalid descriptor")
	}
	if err := unix.Close(mismatchDescriptor); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentRecordPublicationCleansFailedTransitions(t *testing.T) {
	root := shortTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })
	generation, generationID, err := createRuntimeAttachmentGeneration(rootDescriptor, "task-record-failed-publication")
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation, GenerationID: generationID,
		RelaySeed: runtimeRelaySeedForTest(0x52),
	}
	if _, err := publishRuntimeAttachmentIdentity(
		rootDescriptor, "invalid handle", record, nil, nil,
	); err == nil {
		t.Fatal("record publication accepted an invalid task handle")
	}
	taskHandle := "task-record-failed-publication"
	removeTemporary := func() {
		entries, readErr := os.ReadDir(root)
		if readErr != nil {
			t.Fatal(readErr)
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == "" && len(entry.Name()) > 0 {
				continue
			}
			if len(entry.Name()) > len(runtimeAttachmentIdentitySuffix) &&
				filepath.Base(entry.Name()) != taskHandle+runtimeAttachmentIdentitySuffix {
				_ = os.Remove(filepath.Join(root, entry.Name()))
			}
		}
	}
	if _, err := publishRuntimeAttachmentIdentity(rootDescriptor, taskHandle, record, nil, removeTemporary); err == nil {
		t.Fatal("record publication succeeded after its temporary authority was removed")
	}

	oversizedTask := "task-record-oversized"
	oversizedName, err := runtimeAttachmentIdentityName(oversizedTask)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, oversizedName), make([]byte, 408), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := readRuntimeAttachmentIdentityRecord(rootDescriptor, oversizedTask); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("readRuntimeAttachmentIdentityRecord(oversized) error = %v", err)
	}
	unsafeName := "unsafe-record"
	if err := os.WriteFile(filepath.Join(root, unsafeName), []byte("unsafe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !runtimeAttachmentRecordOpenFailureIsUnsafe(rootDescriptor, unsafeName, unix.ESTALE) {
		t.Fatal("unsafe regular record was not classified as task scoped")
	}

	encoded := formatRuntimeAttachmentIdentityRecord(record)
	malformedParts := []byte(encoded)
	for index := range malformedParts {
		if malformedParts[index] == ':' {
			malformedParts[index] = '0'
			break
		}
	}
	if _, err := parseRuntimeAttachmentIdentityRecord(string(malformedParts)); err == nil {
		t.Fatal("fixed-width record with a missing separator parsed")
	}
}

func TestRuntimeAttachmentListenerClosesScopedFailures(t *testing.T) {
	workspace, err := filepath.EvalSymlinks(shortTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 11, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*runtimeAttachmentCoordinator, *application.RuntimeAttachmentPreparationRequest, *application.PreparedRuntimeAttachment)
	}{
		{name: "invalid endpoint", mutate: func(
			_ *runtimeAttachmentCoordinator,
			request *application.RuntimeAttachmentPreparationRequest,
			_ *application.PreparedRuntimeAttachment,
		) {
			request.BriefRevision = 0
		}},
		{name: "relay identity mismatch", mutate: func(
			_ *runtimeAttachmentCoordinator,
			_ *application.RuntimeAttachmentPreparationRequest,
			attachment *application.PreparedRuntimeAttachment,
		) {
			attachment.RelayIdentity = "different-relay-identity"
		}},
		{name: "closed relocation", mutate: func(
			coordinator *runtimeAttachmentCoordinator,
			_ *application.RuntimeAttachmentPreparationRequest,
			_ *application.PreparedRuntimeAttachment,
		) {
			var server *reporter.RuntimeServer
			coordinator.afterRuntimeSocketListen = func(observed *reporter.RuntimeServer) error {
				server = observed
				return nil
			}
			coordinator.afterRuntimeDirectoryPublish = func() error { return server.Close() }
		}},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(shortTempDir(t), "runtime")
			coordinator := runtimeTransitionCoordinator(t, root, &runtimeAttachmentRecoveryStore{}, now)
			taskHandle := "task-listener-edge-" + string(rune('a'+index))
			request := runtimeAttachmentRequest(t, workspace, taskHandle)
			attachment := application.PreparedRuntimeAttachment{
				Kind:       application.RuntimeAttachmentUnixSocket,
				SourcePath: filepath.Join(root, taskHandle, "attachment.sock"),
			}
			test.mutate(coordinator, &request, &attachment)
			if entry, err := coordinator.listenRuntimeAttachment(request, attachment); err == nil || entry != nil {
				t.Fatalf("listenRuntimeAttachment() = %#v, %v", entry, err)
			}
		})
	}
}

func TestRuntimeAttachmentReleaseReplayVerifiesIsolatedNamespace(t *testing.T) {
	root := filepath.Join(shortTempDir(t), "runtime")
	now := time.Date(2026, time.August, 17, 8, 0, 0, 0, time.UTC)
	coordinator := runtimeTransitionCoordinator(t, root, &runtimeAttachmentRecoveryStore{}, now)
	taskHandle := "task-release-isolated-replay"
	taskRoot := filepath.Join(root, taskHandle)
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	task := runtimeAttachmentRecoverableTask(t, now, taskHandle)
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	relaySeed := runtimeRelaySeedForTest(0x41)
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: filepath.Join(taskRoot, "attachment.sock"),
		Brief:      brief,
		Reporter:   &reporter.Client{},
		RelaySeed:  relaySeed[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	socketIdentity, err := server.SocketIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.persistRuntimeAttachmentIdentity(taskHandle, socketIdentity); err != nil {
		t.Fatal(err)
	}
	pinned, record, err := coordinator.pinRuntimeAttachmentRelease(taskHandle)
	if err != nil {
		t.Fatal(err)
	}
	isolate, err := isolatePinnedRuntimeAttachmentRelease(pinned, record)
	if err != nil {
		t.Fatal(err)
	}
	replay := isolate
	replay.Stage = runtimeAttachmentReleaseIntent
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, replay, &isolate, nil); err != nil {
		t.Fatal(err)
	}
	verified, err := isolatePinnedRuntimeAttachmentRelease(pinned, replay)
	if err != nil || verified.Stage != runtimeAttachmentReleasing {
		t.Fatalf("isolatePinnedRuntimeAttachmentRelease(replay) = %#v, %v", verified, err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentReleaseNamespaceRejectsChangedAuthority(t *testing.T) {
	now := time.Date(2026, time.August, 17, 12, 0, 0, 0, time.UTC)
	root := filepath.Join(shortTempDir(t), "runtime")
	coordinator := runtimeTransitionCoordinator(t, root, &runtimeAttachmentRecoveryStore{}, now)
	if pinned, _, err := coordinator.pinRuntimeAttachmentRelease("task-release-record-missing"); err == nil || pinned != nil {
		t.Fatalf("pinRuntimeAttachmentRelease(missing) = %#v, %v", pinned, err)
	}
	if _, err := preparePinnedRuntimeAttachmentClose(nil, nil, runtimeAttachmentIdentityRecord{}, nil); err == nil {
		t.Fatal("preparePinnedRuntimeAttachmentClose accepted missing release authority")
	}
	missingRoot := filepath.Join(shortTempDir(t), "runtime")
	missingCoordinator := runtimeTransitionCoordinator(t, missingRoot, &runtimeAttachmentRecoveryStore{}, now)
	if err := os.Remove(missingRoot); err != nil {
		t.Fatal(err)
	}
	if pinned, _, err := missingCoordinator.pinRuntimeAttachmentRelease("task-release-root-missing"); err == nil || pinned != nil {
		t.Fatalf("pinRuntimeAttachmentRelease(missing root) = %#v, %v", pinned, err)
	}

	taskHandle := "task-release-authority-changed"
	taskPath := filepath.Join(root, taskHandle)
	if err := os.Mkdir(taskPath, 0o700); err != nil {
		t.Fatal(err)
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		t.Fatalf("pinTaskRuntimeDirectory() = %#v, %t, %v", pinned, missing, err)
	}
	actualIdentity := pinned.taskIdentity
	pinned.taskIdentity.Inode++
	if _, err := isolatePinnedRuntimeAttachmentRelease(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent,
	}); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("isolatePinnedRuntimeAttachmentRelease(changed identity) error = %v", err)
	}
	pinned.taskIdentity = actualIdentity
	releaseName := runtimeAttachmentReleaseName(taskHandle)
	if err := os.Rename(taskPath, filepath.Join(root, releaseName)); err != nil {
		t.Fatal(err)
	}
	pinned.directoryName = releaseName
	wrongTask := actualIdentity
	wrongTask.Inode++
	if _, err := isolatePinnedRuntimeAttachmentRelease(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent, Task: wrongTask,
	}); !errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("isolatePinnedRuntimeAttachmentRelease(replay mismatch) error = %v", err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	invalidDescriptor := &pinnedTaskRuntimeDirectory{
		taskDescriptor: -1, taskHandle: taskHandle, directoryName: releaseName,
	}
	if _, err := isolatePinnedRuntimeAttachmentRelease(invalidDescriptor, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent,
	}); err == nil {
		t.Fatal("release replay accepted an invalid directory descriptor")
	}

	publicationTask := "task-release-publication-missing"
	publicationName := runtimeAttachmentReleaseName(publicationTask)
	publicationPath := filepath.Join(root, publicationName)
	if err := os.Mkdir(publicationPath, 0o700); err != nil {
		t.Fatal(err)
	}
	task := runtimeAttachmentRecoverableTask(t, now, publicationTask)
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	relaySeed := runtimeRelaySeedForTest(0x62)
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: filepath.Join(publicationPath, "attachment.sock"), Brief: brief,
		Reporter: &reporter.Client{}, RelaySeed: relaySeed[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	socketIdentity, err := server.SocketIdentity()
	if err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	publicationDescriptor, publicationIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, publicationName)
	if err != nil || missing {
		t.Fatal(err)
	}
	publicationPinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor, taskDescriptor: publicationDescriptor,
		taskHandle: publicationTask, directoryName: publicationName, taskIdentity: publicationIdentity,
	}
	if _, err := isolatePinnedRuntimeAttachmentRelease(publicationPinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent, Task: publicationIdentity, Socket: socketIdentity,
	}); err == nil {
		t.Fatal("release replay published without a durable prior record")
	}
	if err := publicationPinned.close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentCleanupSurfacesTransitionFailures(t *testing.T) {
	now := time.Date(2026, time.August, 17, 13, 0, 0, 0, time.UTC)
	missingRoot := filepath.Join(shortTempDir(t), "runtime")
	coordinator := runtimeTransitionCoordinator(t, missingRoot, &runtimeAttachmentRecoveryStore{}, now)
	if err := os.Remove(missingRoot); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.removeTaskRuntimeDirectory("task-cleanup-root-missing"); err == nil {
		t.Fatal("removeTaskRuntimeDirectory accepted a missing runtime root")
	}

	root := shortTempDir(t)
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unix.Close(rootDescriptor) })
	taskHandle := "task-cleanup-transition-failure"
	if err := os.Mkdir(filepath.Join(root, taskHandle), 0o700); err != nil {
		t.Fatal(err)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, taskHandle)
	if err != nil || missing {
		t.Fatal(err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor,
		taskDescriptor:        taskDescriptor,
		taskHandle:            taskHandle,
		directoryName:         "missing-directory-name",
		taskIdentity:          taskIdentity,
	}
	if err := removePinnedTaskRuntimeDirectory(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound, Task: taskIdentity,
	}); err == nil {
		t.Fatal("directory-bound cleanup accepted a missing publication path")
	}
	closedRootDescriptor, err := unix.Dup(rootDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	if err := unix.Close(closedRootDescriptor); err != nil {
		t.Fatal(err)
	}
	pinned.runtimeRootDescriptor = closedRootDescriptor
	pinned.directoryName = taskHandle
	if err := removePinnedTaskRuntimeDirectory(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentDirectoryBound, Task: taskIdentity,
		Generation:   reporter.RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3},
		GenerationID: [16]byte{1},
	}); err == nil {
		t.Fatal("directory-bound cleanup accepted unavailable generation authority")
	}
	pinned.runtimeRootDescriptor = rootDescriptor
	if err := removePinnedTaskRuntimeDirectory(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentActive, Task: taskIdentity,
	}); err == nil {
		t.Fatal("active cleanup accepted a missing durable identity record")
	}
	pinned.directoryName = "unexpected-release-location"
	if err := removePinnedTaskRuntimeDirectory(pinned, runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent, Task: taskIdentity,
	}); err == nil {
		t.Fatal("release cleanup accepted an ambiguous namespace")
	}
	if err := unix.Close(taskDescriptor); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentCoordinatorRejectsInactiveReleaseAndUnavailableEntry(t *testing.T) {
	root := filepath.Join(shortTempDir(t), "runtime")
	coordinator := runtimeTransitionCoordinator(t, root, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- coordinator.Run(ctx) }()
	select {
	case <-coordinator.recoveryReady:
	case <-time.After(5 * time.Second):
		t.Fatal("runtime attachment recovery did not become ready")
	}
	releaseReady := make(chan error, 1)
	coordinator.releases <- runtimeAttachmentRelease{ready: releaseReady}
	if err := <-releaseReady; err == nil {
		t.Fatal("inactive runtime attachment server was released")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run(canceled) error = %v", err)
	}

	workspace, err := filepath.EvalSymlinks(shortTempDir(t))
	if err != nil {
		t.Fatal(err)
	}
	request := runtimeAttachmentRequest(t, workspace, "task-runtime-entry-unavailable")
	ready := make(chan struct{})
	close(ready)
	coordinator = &runtimeAttachmentCoordinator{
		recoveryReady: ready,
		entries: map[string]*runtimeAttachmentEntry{
			request.TaskHandle: &runtimeAttachmentEntry{request: request, state: runtimeAttachmentEntryState(255)},
		},
		runtimeAttachmentRefusals: make(map[string]struct{}),
	}
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err == nil {
		t.Fatal("prepare accepted an unavailable runtime attachment entry")
	}
	coordinator.runtimeAttachmentRefusals[request.TaskHandle] = struct{}{}
	delete(coordinator.entries, request.TaskHandle)
	if _, err := coordinator.PrepareRuntimeAttachment(context.Background(), request); err == nil {
		t.Fatal("prepare accepted refused filesystem authority")
	}
}

func TestServiceRunCoversOwnedAttachmentAndCompositionBoundaries(t *testing.T) {
	root := shortTempDir(t)
	if err := Run(context.Background(), Config{
		DatabasePath: filepath.Join(root, "partial.db"), SocketPath: filepath.Join(root, "partial.sock"),
		RepositoryComposition: &RepositoryComposition{},
	}); err == nil {
		t.Fatal("Run accepted a partial installed composition")
	}

	ready := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	configuration, err := composeInstalledRuntime(
		context.Background(), installedServiceConfig(t, shortTempDir(t)),
	)
	if err != nil {
		t.Fatal(err)
	}
	configuration.RepositoryComposition = nil
	configuration.ComisComposition = nil
	configuration.CodexComposition = nil
	configuration.ClaudeComposition = nil
	configuration.ValidationComposition = nil
	configuration.ForgeComposition = nil
	configuration.cleanupRemover = nil
	configuration.cleanupForge = nil
	configuration.Ready = func() { close(ready) }
	go func() {
		done <- Run(ctx, configuration)
	}()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run(runtime attachments) before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run(runtime attachments) did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run(runtime attachments) cancellation error = %v", err)
	}
}
