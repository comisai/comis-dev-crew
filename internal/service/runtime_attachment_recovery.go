package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func (coordinator *runtimeAttachmentCoordinator) recoverRuntimeAttachments(ctx context.Context) ([]*reporter.RuntimeServer, error) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.entries) != 0 {
		return nil, errors.New("recover runtime attachments: coordinator is already populated")
	}
	if err := coordinator.recoverRuntimeRelayIdentityUpgrades(ctx); err != nil {
		return nil, err
	}
	if err := coordinator.recoverTaskPreparationIntents(ctx); err != nil {
		return nil, err
	}
	tasks, err := coordinator.store.ListTasks(ctx)
	if err != nil {
		return nil, fmt.Errorf("recover runtime attachments: list tasks: %w", err)
	}
	servers := make([]*reporter.RuntimeServer, 0, len(tasks))
	for _, task := range tasks {
		if _, refused := coordinator.runtimeRelayIdentityRefusals[task.Handle]; refused {
			continue
		}
		if task.State == domain.TaskCleaned {
			if err := coordinator.removeTaskRuntimeDirectory(task.Handle); err != nil {
				return nil, errors.Join(errors.New("recover runtime attachments: cleaned task runtime directory remains"), err)
			}
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, closeRuntimeServers(servers))
		}
		cleanup, cleanupFound, err := coordinator.store.GetTaskCleanupRecord(ctx, task.Handle)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("recover runtime attachments: read cleanup posture: %w", err), closeRuntimeServers(servers))
		}
		if cleanupFound {
			if cleanup.TaskHandle != task.Handle {
				return nil, errors.Join(errors.New("recover runtime attachments: durable cleanup target differs"), closeRuntimeServers(servers))
			}
			switch cleanup.Stage {
			case application.CleanupPrepared:
			case application.CleanupHostReleased, application.CleanupRemovalAuthorized, application.CleanupCompleted:
				if err := coordinator.removeTaskRuntimeDirectory(task.Handle); err != nil {
					return nil, errors.Join(errors.New("recover runtime attachments: released task runtime directory remains"), err, closeRuntimeServers(servers))
				}
				continue
			default:
				return nil, errors.Join(errors.New("recover runtime attachments: durable cleanup posture is invalid"), closeRuntimeServers(servers))
			}
		} else if task.State == domain.TaskCleanupHeld {
			return nil, errors.Join(errors.New("recover runtime attachments: held task cleanup posture is unavailable"), closeRuntimeServers(servers))
		}
		preparation, err := coordinator.store.GetManagedRunPreparation(ctx, task.Handle)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("recover runtime attachments: read preparation: %w", err), closeRuntimeServers(servers))
		}
		expectedSource := filepath.Join(coordinator.runtimeRoot, task.Handle, "attachment.sock")
		if preparation.RequestedAttachment.Kind != application.RuntimeAttachmentUnixSocket ||
			preparation.RequestedAttachment.SourcePath != expectedSource {
			return nil, errors.Join(errors.New("recover runtime attachments: durable attachment source differs"), closeRuntimeServers(servers))
		}
		brief, err := task.RenderWorkerBrief()
		if err != nil {
			return nil, errors.Join(fmt.Errorf("recover runtime attachments: render brief: %w", err), closeRuntimeServers(servers))
		}
		operationDigest := fmt.Sprintf("%x", sha256.Sum256([]byte("runtime-recovery\x00"+task.Handle)))
		request := application.RuntimeAttachmentPreparationRequest{
			OperationID: "runtime-recovery-" + operationDigest[:32], TaskHandle: task.Handle,
			BriefRevision: task.BriefRevision, BriefRevisionHash: task.BriefRevisionHash,
			Brief: brief, WorkingDirectory: preparation.RequestedWorkspaceRoot,
		}
		if err := validateRuntimeAttachmentPreparation(request); err != nil {
			return nil, errors.Join(fmt.Errorf("recover runtime attachments: %w", err), closeRuntimeServers(servers))
		}
		if err := coordinator.removeTaskRuntimeDirectory(task.Handle); err != nil {
			return nil, errors.Join(errors.New("recover runtime attachments: prior attachment cannot be released"), err, closeRuntimeServers(servers))
		}
		entry, err := coordinator.listenRuntimeAttachment(request, preparation.RequestedAttachment)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("recover runtime attachments: %w", err), closeRuntimeServers(servers))
		}
		if task.ExecutionAttachmentID != "" {
			operationID, operationErr := application.RuntimeLaunchAcknowledgementOperationID(task.Handle)
			binding := application.RuntimeAttachmentBindingRequest{
				TaskHandle: task.Handle, ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
				ExecutionAttachmentID: task.ExecutionAttachmentID, AttachmentTargetName: task.AttachmentTargetName,
				LaunchOperationID: operationID, Acknowledger: coordinator.acknowledger,
			}
			if operationErr != nil || validateRuntimeAttachmentBinding(binding) != nil || bindRuntimeAttachmentEntry(entry, binding) != nil {
				return nil, errors.Join(errors.New("recover runtime attachments: durable activation binding is invalid"), entry.server.Close(), closeRuntimeServers(servers))
			}
		}
		coordinator.entries[task.Handle] = entry
		servers = append(servers, entry.server)
	}
	return servers, nil
}
