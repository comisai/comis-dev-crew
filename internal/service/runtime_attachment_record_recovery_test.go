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

func TestRuntimeAttachmentCoordinator_IsolatesSpecialTaskRecord(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 16, 0, 0, 0, time.UTC)
	affected := runtimeAttachmentRecoverableTask(t, now, "task-runtime-special-record")
	affected.State = domain.TaskCleaned
	affected.StateVersion++
	unaffected := runtimeAttachmentRecoverableTask(t, now, "task-runtime-special-unaffected")
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
	affectedRoot := filepath.Join(runtimeRoot, affected.Handle)
	if err := os.Mkdir(affectedRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	recordName, err := runtimeAttachmentIdentityName(affected.Handle)
	if err != nil {
		t.Fatal(err)
	}
	recordPath := filepath.Join(runtimeRoot, recordName)
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: recordPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() { _ = listener.Close() })
	if err := os.Chmod(recordPath, 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := coordinator.recoverRuntimeAttachments(context.Background())
	if err != nil || len(servers) != 1 {
		t.Fatalf("recoverRuntimeAttachments(special record) = %d, %v", len(servers), err)
	}
	if len(store.taskRefusals) != 1 || store.taskRefusals[0].TaskHandle != affected.Handle {
		t.Fatalf("task refusals = %#v", store.taskRefusals)
	}
	if info, err := os.Lstat(recordPath); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("special record = %#v, %v", info, err)
	}
	if info, err := os.Lstat(affectedRoot); err != nil || !info.IsDir() {
		t.Fatalf("affected runtime directory = %#v, %v", info, err)
	}
	if info, err := os.Lstat(filepath.Join(runtimeRoot, unaffected.Handle, "attachment.sock")); err != nil || info.Mode()&os.ModeSocket == 0 {
		t.Fatalf("unaffected socket = %#v, %v", info, err)
	}
	if err := servers[0].Close(); err != nil {
		t.Fatal(err)
	}
}
