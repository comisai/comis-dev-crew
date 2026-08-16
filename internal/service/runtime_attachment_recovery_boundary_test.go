package service

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestRuntimeAttachmentOpenFailuresKeepResourceErrorsInfrastructureScoped(t *testing.T) {
	if _, _, _, err := openTaskRuntimeDirectory(-1, "task-runtime-invalid-descriptor"); err == nil || errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("openTaskRuntimeDirectory(invalid descriptor) error = %v", err)
	}
	if _, _, _, err := readRuntimeAttachmentIdentityRecord(-1, "task-runtime-invalid-descriptor"); err == nil || errors.Is(err, errRuntimeAttachmentOwnershipUnproven) {
		t.Fatalf("readRuntimeAttachmentIdentityRecord(invalid descriptor) error = %v", err)
	}
}

func TestRuntimeAttachmentRecoveryIsolatesSpecialTaskDirectories(t *testing.T) {
	for _, test := range []struct {
		name    string
		handle  string
		replace func(*testing.T, string, string)
	}{
		{
			name: "symlink", handle: "task-runtime-directory-symlink",
			replace: func(t *testing.T, path, original string) {
				if err := os.Symlink(original, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "regular file", handle: "task-runtime-directory-regular-file",
			replace: func(t *testing.T, path, _ string) {
				if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unsafe directory", handle: "task-runtime-directory-unsafe",
			replace: func(t *testing.T, path, _ string) {
				if err := os.Mkdir(path, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := shortTempDir(t)
			runtimeRoot := filepath.Join(root, "runtime")
			workspace := filepath.Join(root, "workspace")
			if err := os.Mkdir(workspace, 0o700); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, time.August, 17, 17, 0, 0, 0, time.UTC)
			preparedAffected := runtimeAttachmentRecoverableTask(t, now, test.handle)
			affected := preparedAffected
			affected.State = domain.TaskCleaned
			affected.StateVersion++
			unaffected := runtimeAttachmentRecoverableTask(t, now, "task-runtime-directory-unaffected")
			store := &runtimeAttachmentRecoveryStore{
				tasks: []domain.Task{affected, unaffected},
				preparations: map[string]application.ManagedRunPreparation{
					unaffected.Handle: runtimeRecoveryPreparation(runtimeRoot, workspace, unaffected.Handle),
				},
			}
			first := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
			brief, err := preparedAffected.RenderWorkerBrief()
			if err != nil {
				t.Fatal(err)
			}
			entry, err := first.listenRuntimeAttachment(application.RuntimeAttachmentPreparationRequest{
				OperationID: "operation-runtime-directory-special", TaskHandle: preparedAffected.Handle,
				BriefRevision: preparedAffected.BriefRevision, BriefRevisionHash: preparedAffected.BriefRevisionHash,
				Brief: brief, WorkingDirectory: workspace,
			}, runtimeRecoveryPreparation(runtimeRoot, workspace, affected.Handle).RequestedAttachment)
			if err != nil {
				t.Fatal(err)
			}
			if err := entry.server.Close(); err != nil {
				t.Fatal(err)
			}
			canonical := filepath.Join(runtimeRoot, affected.Handle)
			original := canonical + ".original"
			if err := os.Rename(canonical, original); err != nil {
				t.Fatal(err)
			}
			test.replace(t, canonical, original)

			restarted := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
			servers, err := restarted.recoverRuntimeAttachments(context.Background())
			if err != nil || len(servers) != 1 {
				t.Fatalf("recoverRuntimeAttachments(%s) = %d, %v", test.name, len(servers), err)
			}
			if len(store.taskRefusals) != 1 || store.taskRefusals[0].TaskHandle != affected.Handle {
				t.Fatalf("task refusals = %#v", store.taskRefusals)
			}
			if _, err := os.Lstat(canonical); err != nil {
				t.Fatalf("special task path was not preserved: %v", err)
			}
			if info, err := os.Lstat(filepath.Join(runtimeRoot, unaffected.Handle, "attachment.sock")); err != nil || info.Mode()&os.ModeSocket == 0 {
				t.Fatalf("unaffected socket = %#v, %v", info, err)
			}
			if err := servers[0].Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeAttachmentRecoveryClosesServersOnCleanedTaskFailure(t *testing.T) {
	root := shortTempDir(t)
	runtimeRoot := filepath.Join(root, "runtime")
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 17, 17, 30, 0, 0, time.UTC)
	recoverable := runtimeAttachmentRecoverableTask(t, now, "task-runtime-close-on-recovery-error")
	invalidCleaned := domain.Task{Handle: "", State: domain.TaskCleaned}
	store := &runtimeAttachmentRecoveryStore{
		tasks: []domain.Task{recoverable, invalidCleaned},
		preparations: map[string]application.ManagedRunPreparation{
			recoverable.Handle: runtimeRecoveryPreparation(runtimeRoot, workspace, recoverable.Handle),
		},
	}
	coordinator := runtimeTransitionCoordinator(t, runtimeRoot, store, now)
	servers, err := coordinator.recoverRuntimeAttachments(context.Background())
	if err == nil || len(servers) != 0 {
		t.Fatalf("recoverRuntimeAttachments(cleaned failure) = %d, %v", len(servers), err)
	}
	server := coordinator.entries[recoverable.Handle].server
	t.Cleanup(func() { _ = server.Close() })
	connection, dialErr := net.DialTimeout(
		"unix", filepath.Join(runtimeRoot, recoverable.Handle, "attachment.sock"), 100*time.Millisecond,
	)
	if connection != nil {
		_ = connection.Close()
	}
	if dialErr == nil {
		t.Fatal("recovered listener remained reachable after recovery failed")
	}
}

func runtimeRecoveryPreparation(runtimeRoot, workspace, taskHandle string) application.ManagedRunPreparation {
	return application.ManagedRunPreparation{
		RequestedWorkspaceRoot: workspace,
		RequestedAttachment: application.PreparedRuntimeAttachment{
			Kind:          application.RuntimeAttachmentUnixSocket,
			SourcePath:    filepath.Join(runtimeRoot, taskHandle, "attachment.sock"),
			RelayIdentity: runtimeTransitionRelayIdentity(),
		},
	}
}
