package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
	"golang.org/x/sys/unix"
)

func TestRuntimeAttachmentReleaseRecoversAfterCloseBeforeDirectoryStage(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	now := time.Date(2026, time.August, 16, 13, 0, 0, 0, time.UTC)
	task := runtimeAttachmentCleanupHeldTask(t, now, "task-runtime-release-transition")
	store := &runtimeAttachmentRecoveryStore{
		tasks: []domain.Task{task}, cleanupFound: true,
		cleanupRecord: application.TaskCleanupRecord{TaskHandle: task.Handle, Stage: application.CleanupHostReleased},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	close(coordinator.recoveryReady)
	taskRoot := filepath.Join(runtimeRoot, task.Handle)
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(taskRoot, "attachment.sock")
	relaySeed := runtimeRelaySeedForTest(0x21)
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: socketPath, Brief: brief, Reporter: &reporter.Client{},
		RelaySeed: relaySeed[:],
	})
	if err != nil {
		t.Fatal(err)
	}
	identity, err := server.SocketIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if err := coordinator.persistRuntimeAttachmentIdentity(task.Handle, identity); err != nil {
		t.Fatal(err)
	}
	coordinator.entries[task.Handle] = &runtimeAttachmentEntry{
		attachment: application.PreparedRuntimeAttachment{
			Kind: application.RuntimeAttachmentUnixSocket, SourcePath: socketPath,
			RelayIdentity: server.RelayIdentity(),
		},
		server: server,
	}
	crash := errors.New("simulated process stop")
	coordinator.afterRuntimeAttachmentClose = func() error { return crash }
	go func() {
		release := <-coordinator.releases
		release.ready <- nil
	}()
	if err := coordinator.ReleaseRuntimeAttachment(context.Background(), task.Handle); !errors.Is(err, crash) {
		t.Fatalf("ReleaseRuntimeAttachment(simulated stop) error = %v", err)
	}
	if _, err := os.Lstat(taskRoot); !os.IsNotExist(err) {
		t.Fatalf("canonical released task directory error = %v, want isolated", err)
	}
	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(after release stop) = %d, %v", len(servers), err)
	}
	if _, err := os.Lstat(taskRoot); !os.IsNotExist(err) {
		t.Fatalf("released task directory error = %v, want not exist", err)
	}
}

func TestOpenRecordedRuntimeReleasePreservesIsolatedDirectoryWithoutUnrecyclableIdentity(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-release-no-birthtime"
	taskRoot := filepath.Join(runtimeRoot, taskHandle)
	if err := os.Mkdir(taskRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	pinned, missing, err := coordinator.pinTaskRuntimeDirectory(taskHandle)
	if err != nil || missing {
		t.Fatalf("pinTaskRuntimeDirectory() = %#v, %t, %v", pinned, missing, err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleasing, Task: pinned.taskIdentity, RelaySeed: runtimeRelaySeedForTest(0x22),
	}
	record.Task.BirthSec = 0
	record.Task.BirthNsec = 0
	releaseName := runtimeAttachmentReleaseName(taskHandle)
	if err := os.Rename(taskRoot, filepath.Join(runtimeRoot, releaseName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeRoot, releaseName, "changed"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(runtimeRoot, releaseName, "changed")); err != nil {
		t.Fatal(err)
	}
	_ = pinned.close()
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(rootDescriptor, taskHandle, record)
	if err == nil || missing || reopened != nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory() = %#v, %t, %v, want preserved ambiguity", reopened, missing, err)
	}
	if info, statErr := os.Lstat(filepath.Join(runtimeRoot, releaseName)); statErr != nil || !info.IsDir() {
		t.Fatalf("isolated release was not preserved: %#v, %v", info, statErr)
	}
	_ = closeRuntimeRootDescriptor(rootDescriptor)
}

func TestRuntimeAttachmentReleaseBindsPostRenameIdentityWithoutBirthTime(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-release-post-rename"
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
	pinned.taskIdentity, err = runtimeAttachmentDescriptorIdentity(pinned.taskDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleaseIntent, Task: pinned.taskIdentity, Socket: socketIdentity,
		Generation: generation, GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x2a),
	}
	record.Task.BirthSec, record.Task.BirthNsec = 0, 0
	if _, err := publishPinnedRuntimeAttachmentIdentity(pinned, record, nil, nil); err != nil {
		t.Fatal(err)
	}
	pinned.taskIdentity = record.Task
	updated, err := isolatePinnedRuntimeAttachmentRelease(pinned, record)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Stage != runtimeAttachmentReleasing || updated.Task == record.Task ||
		!sameRuntimeAttachmentNode(updated.Task, record.Task) {
		t.Fatalf("post-rename release record = %#v", updated)
	}
	stored, _, found, err := readRuntimeAttachmentIdentityRecord(pinned.runtimeRootDescriptor, taskHandle)
	if err != nil || !found || stored != updated {
		t.Fatalf("stored post-rename release record = %#v, %t, %v", stored, found, err)
	}
	_ = pinned.close()
}

func TestOpenRecordedRuntimeReleaseRejectsMissingGenerationAuthority(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-release-birth-identity"
	if err := os.Mkdir(filepath.Join(runtimeRoot, runtimeAttachmentReleaseName(taskHandle)), 0o700); err != nil {
		t.Fatal(err)
	}
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	descriptor, identity, missing, err := openTaskRuntimeDirectory(rootDescriptor, runtimeAttachmentReleaseName(taskHandle))
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %t, %v", descriptor, identity, missing, err)
	}
	_ = unix.Close(descriptor)
	record := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentReleasing, Task: identity, RelaySeed: runtimeRelaySeedForTest(0x23),
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(rootDescriptor, taskHandle, record)
	if err == nil || missing || reopened != nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(missing generation) = %#v, %t, %v", reopened, missing, err)
	}
	_ = closeRuntimeRootDescriptor(rootDescriptor)
}

func TestRuntimeAttachmentRecoveryReplaysPublishedReplacementDirectory(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 16, 13, 10, 0, 0, time.UTC)
	task := runtimeAttachmentRecoverableTask(t, now, "task-runtime-create-transition")
	taskRoot := filepath.Join(runtimeRoot, task.Handle)
	socketPath := filepath.Join(taskRoot, "attachment.sock")
	store := &runtimeTransitionStore{
		task: task,
		preparation: application.ManagedRunPreparation{
			RequestedWorkspaceRoot: workspace,
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind: application.RuntimeAttachmentUnixSocket, SourcePath: socketPath,
				RelayIdentity: runtimeTransitionRelayIdentity(),
			},
		},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	coordinator.afterRuntimeDirectoryPublish = func() error { return errors.New("simulated process stop") }
	if servers, err := coordinator.recoverRuntimeAttachments(context.Background()); err == nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(publish stop) = %d, %v", len(servers), err)
	}
	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("recoverRuntimeAttachments(publish replay) = %d, %v", len(servers), err)
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentRecoveryReplaysCreationIntent(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 16, 13, 20, 0, 0, time.UTC)
	task := runtimeAttachmentRecoverableTask(t, now, "task-runtime-create-intent")
	store := &runtimeTransitionStore{
		task: task,
		preparation: application.ManagedRunPreparation{
			RequestedWorkspaceRoot: workspace,
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind:          application.RuntimeAttachmentUnixSocket,
				SourcePath:    filepath.Join(runtimeRoot, task.Handle, "attachment.sock"),
				RelayIdentity: runtimeTransitionRelayIdentity(),
			},
		},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	coordinator.afterRuntimeDirectoryCreation = func() error { return errors.New("simulated process stop") }
	if servers, err := coordinator.recoverRuntimeAttachments(context.Background()); err == nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(creation stop) = %d, %v", len(servers), err)
	}
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	record, _, found, readErr := readRuntimeAttachmentIdentityRecord(descriptor, task.Handle)
	_ = closeRuntimeRootDescriptor(descriptor)
	if readErr != nil || !found || record.Stage != runtimeAttachmentCreating || record.Socket.Valid() ||
		!record.Generation.Valid() {
		t.Fatalf("creation intent = %#v, found=%v, err=%v", record, found, readErr)
	}
	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("recoverRuntimeAttachments(creation replay) = %d, %v", len(servers), err)
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentRecoveryReplaysGenerationLinkWithoutBirthTime(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 16, 13, 22, 0, 0, time.UTC)
	task := runtimeAttachmentRecoverableTask(t, now, "task-runtime-generation-link")
	store := &runtimeTransitionStore{
		task: task,
		preparation: application.ManagedRunPreparation{
			RequestedWorkspaceRoot: workspace,
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind:          application.RuntimeAttachmentUnixSocket,
				SourcePath:    filepath.Join(runtimeRoot, task.Handle, "attachment.sock"),
				RelayIdentity: runtimeTransitionRelayIdentity(),
			},
		},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	generation, generationID, err := createRuntimeAttachmentGeneration(rootDescriptor, task.Handle)
	if err != nil {
		t.Fatal(err)
	}
	generation.BirthSec, generation.BirthNsec = 0, 0
	relaySeed := sha256.Sum256([]byte("runtime-relay\x00transition-credential-0123456789abcdef"))
	intent := runtimeAttachmentIdentityRecord{
		Stage: runtimeAttachmentCreatingIntent, Generation: generation,
		GenerationID: generationID, RelaySeed: relaySeed,
	}
	if _, err := publishRuntimeAttachmentIdentity(rootDescriptor, task.Handle, intent, nil, nil); err != nil {
		t.Fatal(err)
	}
	temporaryName := runtimeAttachmentCreationName(task.Handle)
	if err := unix.Mkdirat(rootDescriptor, temporaryName, 0o700); err != nil {
		t.Fatal(err)
	}
	taskDescriptor, taskIdentity, missing, err := openTaskRuntimeDirectory(rootDescriptor, temporaryName)
	if err != nil || missing {
		t.Fatalf("openTaskRuntimeDirectory() = %d, %#v, %t, %v", taskDescriptor, taskIdentity, missing, err)
	}
	pinned := &pinnedTaskRuntimeDirectory{
		runtimeRootDescriptor: rootDescriptor, taskDescriptor: taskDescriptor,
		taskHandle: task.Handle, directoryName: temporaryName, taskIdentity: taskIdentity,
	}
	if _, err := linkRuntimeAttachmentGeneration(pinned, generation, generationID); err != nil {
		t.Fatal(err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("recoverRuntimeAttachments(generation link replay) = %d, %v", len(servers), err)
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAttachmentRecoveryQuarantinesSocketCreatedBeforeIdentityCommit(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 16, 13, 25, 0, 0, time.UTC)
	task := runtimeAttachmentRecoverableTask(t, now, "task-runtime-socket-intent")
	store := &runtimeTransitionStore{
		task: task,
		preparation: application.ManagedRunPreparation{
			RequestedWorkspaceRoot: workspace,
			RequestedAttachment: application.PreparedRuntimeAttachment{
				Kind:          application.RuntimeAttachmentUnixSocket,
				SourcePath:    filepath.Join(runtimeRoot, task.Handle, "attachment.sock"),
				RelayIdentity: runtimeTransitionRelayIdentity(),
			},
		},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	var interrupted *reporter.RuntimeServer
	coordinator.afterRuntimeSocketListen = func(server *reporter.RuntimeServer) error {
		interrupted = server
		return errors.New("simulated process stop")
	}
	if servers, err := coordinator.recoverRuntimeAttachments(context.Background()); err == nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(socket stop) = %d, %v", len(servers), err)
	}
	if interrupted == nil {
		t.Fatal("socket creation stop did not capture server")
	}
	t.Cleanup(func() { _ = interrupted.Close() })
	descriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	record, _, found, readErr := readRuntimeAttachmentIdentityRecord(descriptor, task.Handle)
	_ = closeRuntimeRootDescriptor(descriptor)
	if readErr != nil || !found || record.Stage != runtimeAttachmentCreating || record.Socket.Valid() ||
		!record.Generation.Valid() {
		t.Fatalf("pre-socket identity = %#v, found=%v, err=%v", record, found, readErr)
	}
	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("recoverRuntimeAttachments(socket replay) = %d, %v", len(servers), err)
	}
	var preservedSocket bool
	if err := filepath.WalkDir(runtimeRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == filepath.Join(runtimeRoot, task.Handle, "attachment.sock") {
			return nil
		}
		if info, err := entry.Info(); err == nil && info.Mode()&os.ModeSocket != 0 {
			preservedSocket = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !preservedSocket {
		t.Fatal("uncommitted socket was not preserved in quarantine")
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordedRuntimeDirectoryUsesGenerationAcrossCtimeChanges(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, &runtimeAttachmentRecoveryStore{}, time.Now().UTC())
	taskHandle := "task-runtime-generation-proof"
	if err := os.Mkdir(filepath.Join(runtimeRoot, taskHandle), 0o700); err != nil {
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
		Stage: runtimeAttachmentCreating, Task: pinned.taskIdentity,
		Generation: generation, GenerationID: generationID, RelaySeed: runtimeRelaySeedForTest(0x2b),
	}
	record.Task.BirthSec, record.Task.BirthNsec = 0, 0
	changed := filepath.Join(runtimeRoot, taskHandle, "changed")
	if err := os.WriteFile(changed, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(changed); err != nil {
		t.Fatal(err)
	}
	_ = pinned.close()
	rootDescriptor, err := coordinator.pinRuntimeRoot()
	if err != nil {
		t.Fatal(err)
	}
	reopened, missing, err := openRecordedTaskRuntimeDirectory(rootDescriptor, taskHandle, record)
	if err != nil || missing || reopened == nil {
		t.Fatalf("openRecordedTaskRuntimeDirectory(generation) = %#v, %t, %v", reopened, missing, err)
	}
	_ = reopened.close()
}

func TestRuntimeAttachmentRecoveryRemovesUncommittedPreparationPublication(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 16, 15, 10, 0, 0, time.UTC)
	task := runtimeAttachmentRecoverableTask(t, now, "task-runtime-uncommitted-intent")
	intent := application.TaskPreparationIntent{
		OperationID: "operation-runtime-uncommitted-intent", TaskHandle: task.Handle,
		SubjectDigest: strings.Repeat("a", 64), CreatedAt: now,
	}
	if _, err := store.RecordTaskPreparationIntent(context.Background(), intent); err != nil {
		t.Fatal(err)
	}
	first := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	brief, err := task.RenderWorkerBrief()
	if err != nil {
		t.Fatal(err)
	}
	attachment := application.PreparedRuntimeAttachment{
		Kind:          application.RuntimeAttachmentUnixSocket,
		SourcePath:    filepath.Join(runtimeRoot, task.Handle, "attachment.sock"),
		RelayIdentity: runtimeTransitionRelayIdentity(),
	}
	entry, err := first.listenRuntimeAttachment(application.RuntimeAttachmentPreparationRequest{
		OperationID: intent.OperationID, TaskHandle: task.Handle,
		BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
		Brief: brief, WorkingDirectory: workspace,
	}, attachment)
	if err != nil {
		t.Fatal(err)
	}
	first.entries[task.Handle] = entry
	if err := first.closeRuntimeServerForShutdown(entry.server); err != nil {
		t.Fatal(err)
	}
	restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := restarted.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(uncommitted intent) = %d, %v", len(servers), err)
	}
	if _, err := os.Lstat(filepath.Join(runtimeRoot, task.Handle)); !os.IsNotExist(err) {
		t.Fatalf("uncommitted runtime publication error = %v, want absent", err)
	}
}

func runtimeTransitionRelayIdentity() string {
	seed := sha256.Sum256([]byte("runtime-relay\x00transition-credential-0123456789abcdef"))
	privateKey := ed25519.NewKeyFromSeed(seed[:])
	return hex.EncodeToString(privateKey.Public().(ed25519.PublicKey))
}

func runtimeTransitionCoordinator(
	t *testing.T,
	runtimeRoot string,
	store runtimeAttachmentStore,
	now time.Time,
) *runtimeAttachmentCoordinator {
	t.Helper()
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: runtimeRoot, Store: store, Clock: func() time.Time { return now },
		NewCredential:           func() (string, error) { return "transition-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return coordinator
}

func runtimeAttachmentRecoverableTask(t *testing.T, now time.Time, handle string) domain.Task {
	t.Helper()
	task := domain.Task{
		SchemaVersion: 1, Handle: handle, State: domain.TaskPrepared,
		ServiceInstanceID: "service-instance-runtime-transition", Shape: domain.ShapeScout,
		RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40), BriefRevision: 1,
		AcceptanceCriteria: []string{"Recover the reporter attachment."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "codex-reviewed",
		StateVersion: 1, CreatedAt: now, UpdatedAt: now,
	}
	pinned, err := task.PinBriefRevision()
	if err != nil || pinned.Validate() != nil {
		t.Fatalf("recoverable task = %#v, %v", pinned, err)
	}
	return pinned
}

type runtimeTransitionStore struct {
	task        domain.Task
	preparation application.ManagedRunPreparation
}

func (store *runtimeTransitionStore) ListTasks(context.Context) ([]domain.Task, error) {
	return []domain.Task{store.task}, nil
}

func (*runtimeTransitionStore) ListTaskPreparationIntents(context.Context) ([]application.TaskPreparationIntent, error) {
	return nil, nil
}

func (*runtimeTransitionStore) ListRuntimeRelayIdentityUpgrades(context.Context) ([]application.RuntimeRelayIdentityUpgrade, error) {
	return nil, nil
}

func (*runtimeTransitionStore) ListRuntimeRelayIdentityRefusals(context.Context) ([]application.RuntimeRelayIdentityRefusal, error) {
	return nil, nil
}

func (*runtimeTransitionStore) CompleteRuntimeRelayIdentityUpgrade(
	context.Context,
	application.RuntimeRelayIdentityUpgrade,
) error {
	return nil
}

func (*runtimeTransitionStore) RefuseRuntimeRelayIdentityUpgrade(
	context.Context,
	application.RuntimeRelayIdentityUpgrade,
	time.Time,
) error {
	return nil
}

func (store *runtimeTransitionStore) GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error) {
	return store.preparation, nil
}

func (*runtimeTransitionStore) GetTaskCleanupRecord(context.Context, string) (application.TaskCleanupRecord, bool, error) {
	return application.TaskCleanupRecord{}, false, nil
}

func (*runtimeTransitionStore) CommitReport(context.Context, application.ReportMutation) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}
