package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

type runtimeAttachmentStore interface {
	application.ReportMutationStore
	ListRuntimeRelayIdentityUpgrades(context.Context) ([]application.RuntimeRelayIdentityUpgrade, error)
	CompleteRuntimeRelayIdentityUpgrade(context.Context, application.RuntimeRelayIdentityUpgrade) error
	ListTaskPreparationIntents(context.Context) ([]application.TaskPreparationIntent, error)
	ListTasks(context.Context) ([]domain.Task, error)
	GetManagedRunPreparation(context.Context, string) (application.ManagedRunPreparation, error)
	GetTaskCleanupRecord(context.Context, string) (application.TaskCleanupRecord, bool, error)
}

type runtimeAttachmentCoordinatorConfig struct {
	RuntimeRoot             string
	Store                   runtimeAttachmentStore
	Clock                   application.Clock
	NewCredential           func() (string, error)
	NewAttentionOperationID func() (string, error)
}

type runtimeAttachmentRegistration struct {
	server *reporter.RuntimeServer
	ready  chan error
}

type runtimeAttachmentResult struct {
	server *reporter.RuntimeServer
	err    error
}

type runtimeAttachmentEntry struct {
	request    application.RuntimeAttachmentPreparationRequest
	attachment application.PreparedRuntimeAttachment
	server     *reporter.RuntimeServer
	binding    *application.RuntimeAttachmentBindingRequest
}

type runtimeAttachmentCoordinator struct {
	runtimeRoot                   string
	runtimeRootIdentity           reporter.RuntimeSocketIdentity
	store                         runtimeAttachmentStore
	reportSink                    *application.ReportSink
	newCredential                 func() (string, error)
	newAttentionOperationID       func() (string, error)
	registrations                 chan runtimeAttachmentRegistration
	recoveryReady                 chan struct{}
	mu                            sync.Mutex
	entries                       map[string]*runtimeAttachmentEntry
	acknowledger                  application.WorkerLaunchAcknowledger
	attentionResponses            comiswire.AttentionResponseReceiver
	releasedServerStopped         func(*reporter.RuntimeServer)
	afterRuntimeAttachmentClose   func() error
	afterRuntimeDirectoryCreation func() error
	afterRuntimeDirectoryPublish  func() error
	recoveryErr                   error
}

func newRuntimeAttachmentCoordinator(config runtimeAttachmentCoordinatorConfig) (*runtimeAttachmentCoordinator, error) {
	if config.Store == nil || config.Clock == nil || config.NewCredential == nil || config.NewAttentionOperationID == nil {
		return nil, errors.New("create runtime attachment coordinator: store, clock, and identity sources are required")
	}
	runtimeRoot, err := ensureOwnedRuntimeRoot(config.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	runtimeRootIdentity, err := runtimeAttachmentDirectoryIdentity(runtimeRoot)
	if err != nil {
		return nil, err
	}
	sink, err := application.NewReportSink(application.ReportSinkConfig{Store: config.Store, Clock: config.Clock})
	if err != nil {
		return nil, fmt.Errorf("create runtime attachment coordinator report sink: %w", err)
	}
	return &runtimeAttachmentCoordinator{
		runtimeRoot: runtimeRoot, runtimeRootIdentity: runtimeRootIdentity,
		store: config.Store, reportSink: sink, newCredential: config.NewCredential,
		newAttentionOperationID: config.NewAttentionOperationID,
		registrations:           make(chan runtimeAttachmentRegistration), recoveryReady: make(chan struct{}),
		entries: make(map[string]*runtimeAttachmentEntry),
	}, nil
}

// SetRecoveryAcknowledger supplies the application authority needed to restore
// a durable activation binding before any recovered worker can acknowledge it.
func (coordinator *runtimeAttachmentCoordinator) SetRecoveryAcknowledger(acknowledger application.WorkerLaunchAcknowledger) error {
	if acknowledger == nil {
		return errors.New("configure runtime attachment recovery: launch acknowledger is required")
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.acknowledger != nil {
		return errors.New("configure runtime attachment recovery: launch acknowledger is already set")
	}
	coordinator.acknowledger = acknowledger
	return nil
}

func (coordinator *runtimeAttachmentCoordinator) PrepareRuntimeAttachment(
	ctx context.Context,
	request application.RuntimeAttachmentPreparationRequest,
) (application.PreparedRuntimeAttachment, error) {
	if ctx == nil {
		return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.PreparedRuntimeAttachment{}, err
	}
	if err := validateRuntimeAttachmentPreparation(request); err != nil {
		return application.PreparedRuntimeAttachment{}, err
	}
	select {
	case <-coordinator.recoveryReady:
	case <-ctx.Done():
		return application.PreparedRuntimeAttachment{}, ctx.Err()
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if coordinator.recoveryErr != nil {
		return application.PreparedRuntimeAttachment{}, coordinator.recoveryErr
	}
	if existing := coordinator.entries[request.TaskHandle]; existing != nil {
		if existing.request != request {
			return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: task binding conflicts")
		}
		return existing.attachment, nil
	}
	taskRoot := filepath.Join(coordinator.runtimeRoot, request.TaskHandle)
	attachment := application.PreparedRuntimeAttachment{
		Kind: application.RuntimeAttachmentUnixSocket, SourcePath: filepath.Join(taskRoot, "attachment.sock"),
	}
	if len([]byte(attachment.SourcePath)) > 100 {
		return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: socket path exceeds the Unix bound")
	}
	entry, err := coordinator.listenRuntimeAttachment(request, attachment)
	if err != nil {
		return application.PreparedRuntimeAttachment{}, err
	}
	if err := entry.attachment.Validate(); err != nil {
		return application.PreparedRuntimeAttachment{}, errors.Join(
			errors.New("prepare runtime attachment: relay identity is invalid"), entry.server.Close(),
		)
	}
	coordinator.entries[request.TaskHandle] = entry
	registration := runtimeAttachmentRegistration{server: entry.server, ready: make(chan error, 1)}
	select {
	case coordinator.registrations <- registration:
	case <-ctx.Done():
		delete(coordinator.entries, request.TaskHandle)
		return application.PreparedRuntimeAttachment{}, errors.Join(ctx.Err(), entry.server.Close())
	}
	select {
	case err := <-registration.ready:
		if err != nil {
			delete(coordinator.entries, request.TaskHandle)
			return application.PreparedRuntimeAttachment{}, errors.Join(err, entry.server.Close())
		}
		return entry.attachment, nil
	case <-ctx.Done():
		delete(coordinator.entries, request.TaskHandle)
		return application.PreparedRuntimeAttachment{}, errors.Join(ctx.Err(), entry.server.Close())
	}
}

func (coordinator *runtimeAttachmentCoordinator) BindRuntimeAttachment(
	ctx context.Context,
	request application.RuntimeAttachmentBindingRequest,
) error {
	if ctx == nil {
		return errors.New("bind runtime attachment: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-coordinator.recoveryReady:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := validateRuntimeAttachmentBinding(request); err != nil {
		return err
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	entry := coordinator.entries[request.TaskHandle]
	if entry == nil {
		return errors.New("bind runtime attachment: prepared socket is unavailable")
	}
	return bindRuntimeAttachmentEntry(entry, request)
}

func bindRuntimeAttachmentEntry(entry *runtimeAttachmentEntry, request application.RuntimeAttachmentBindingRequest) error {
	if entry.binding != nil {
		prior := *entry.binding
		prior.Acknowledger = nil
		presented := request
		presented.Acknowledger = nil
		if prior != presented {
			return errors.New("bind runtime attachment: activation replay conflicts")
		}
		return nil
	}
	expected := application.LaunchAcknowledgement{
		TaskHandle: request.TaskHandle, ManagedRunID: request.ManagedRunID,
		WorkspaceLeaseID: request.WorkspaceLeaseID, WorkingDirectory: entry.request.WorkingDirectory,
		BriefRevision: entry.request.BriefRevision, BriefRevisionHash: entry.request.BriefRevisionHash,
	}
	if err := entry.server.BindLaunch(reporter.RuntimeLaunchConfig{
		OperationID: request.LaunchOperationID, Expected: expected, Acknowledger: request.Acknowledger,
	}); err != nil {
		return err
	}
	binding := request
	entry.binding = &binding
	return nil
}

func validateRuntimeAttachmentBinding(request application.RuntimeAttachmentBindingRequest) error {
	if domain.ValidateTaskHandle(request.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("managedRunId", request.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("workspaceLeaseId", request.WorkspaceLeaseID) != nil ||
		domain.ValidateAuthorityReference("executionAttachmentId", request.ExecutionAttachmentID) != nil ||
		domain.ValidateOperationID(request.LaunchOperationID) != nil ||
		domain.ValidateAttachmentTargetName(request.AttachmentTargetName) != nil || request.Acknowledger == nil {
		return errors.New("bind runtime attachment: activation binding is invalid")
	}
	return nil
}

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

func (coordinator *runtimeAttachmentCoordinator) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run runtime attachment coordinator: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	results := make(chan runtimeAttachmentResult)
	active := make(map[*reporter.RuntimeServer]struct{})
	recovered, err := coordinator.recoverRuntimeAttachments(ctx)
	coordinator.mu.Lock()
	coordinator.recoveryErr = err
	close(coordinator.recoveryReady)
	coordinator.mu.Unlock()
	if err != nil {
		return err
	}
	for _, server := range recovered {
		active[server] = struct{}{}
		go func(recoveredServer *reporter.RuntimeServer) {
			results <- runtimeAttachmentResult{server: recoveredServer, err: recoveredServer.Serve(context.WithoutCancel(ctx))}
		}(server)
	}
	for {
		select {
		case registration := <-coordinator.registrations:
			active[registration.server] = struct{}{}
			go func(server *reporter.RuntimeServer) {
				results <- runtimeAttachmentResult{server: server, err: server.Serve(context.WithoutCancel(ctx))}
			}(registration.server)
			registration.ready <- nil
		case result := <-results:
			delete(active, result.server)
			if ctx.Err() == nil {
				if coordinator.hasRuntimeServer(result.server) {
					return coordinator.stopServers(active, results, errors.Join(errors.New("runtime attachment server stopped"), result.err))
				}
				if coordinator.releasedServerStopped != nil {
					coordinator.releasedServerStopped(result.server)
				}
				continue
			}
			if len(active) == 0 {
				return nil
			}
		case <-ctx.Done():
			return coordinator.stopServers(active, results, nil)
		}
	}
}

func (coordinator *runtimeAttachmentCoordinator) stopServers(
	active map[*reporter.RuntimeServer]struct{},
	results <-chan runtimeAttachmentResult,
	resultErr error,
) error {
	for server := range active {
		resultErr = errors.Join(resultErr, coordinator.closeRuntimeServerForShutdown(server))
	}
	for range active {
		result := <-results
		if result.err != nil {
			resultErr = errors.Join(resultErr, result.err)
		}
	}
	return resultErr
}

func (coordinator *runtimeAttachmentCoordinator) closeRuntimeServerForShutdown(server *reporter.RuntimeServer) error {
	coordinator.mu.Lock()
	var taskHandle string
	for handle, entry := range coordinator.entries {
		if entry.server == server {
			taskHandle = handle
			break
		}
	}
	var pinned *pinnedTaskRuntimeDirectory
	var record runtimeAttachmentIdentityRecord
	var pinErr error
	if taskHandle != "" {
		pinned, record, pinErr = coordinator.pinRuntimeAttachmentRelease(taskHandle)
		if pinErr == nil {
			record, pinErr = preparePinnedRuntimeAttachmentClose(coordinator, pinned, record, server)
		}
	}
	coordinator.mu.Unlock()
	closeErr := server.Close()
	if pinErr != nil {
		return errors.Join(pinErr, closeErr)
	}
	if pinned == nil {
		return closeErr
	}
	_, stageErr := stagePinnedRuntimeAttachmentDirectory(pinned, record)
	return errors.Join(closeErr, stageErr, pinned.close())
}

var _ application.RuntimeAttachmentCoordinator = (*runtimeAttachmentCoordinator)(nil)
