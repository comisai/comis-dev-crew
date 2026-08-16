package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

type runtimeAttachmentStore interface {
	application.ReportMutationStore
	ListRuntimeRelayIdentityUpgrades(context.Context) ([]application.RuntimeRelayIdentityUpgrade, error)
	ListRuntimeRelayIdentityRefusals(context.Context) ([]application.RuntimeRelayIdentityRefusal, error)
	CompleteRuntimeRelayIdentityUpgrade(context.Context, application.RuntimeRelayIdentityUpgrade) error
	RefuseRuntimeRelayIdentityUpgrade(context.Context, application.RuntimeRelayIdentityUpgrade, time.Time) error
	ListTaskPreparationIntents(context.Context) ([]application.TaskPreparationIntent, error)
	ListRuntimeAttachmentRecoveryRefusals(context.Context) ([]application.RuntimeAttachmentRecoveryRefusal, error)
	RefuseRuntimeAttachmentRecovery(context.Context, application.TaskPreparationIntent, time.Time) error
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

type runtimeAttachmentRelease struct {
	server *reporter.RuntimeServer
	ready  chan error
}

type runtimeAttachmentResult struct {
	server *reporter.RuntimeServer
	err    error
}

type runtimeAttachmentCoordinator struct {
	runtimeRoot                            string
	runtimeRootIdentity                    reporter.RuntimeSocketIdentity
	store                                  runtimeAttachmentStore
	clock                                  application.Clock
	reportSink                             *application.ReportSink
	newCredential                          func() (string, error)
	newAttentionOperationID                func() (string, error)
	registrations                          chan runtimeAttachmentRegistration
	releases                               chan runtimeAttachmentRelease
	recoveryReady                          chan struct{}
	runDone                                chan struct{}
	mu                                     sync.Mutex
	entries                                map[string]*runtimeAttachmentEntry
	runtimeAttachmentRefusals              map[string]struct{}
	acknowledger                           application.WorkerLaunchAcknowledger
	attentionResponses                     comiswire.AttentionResponseReceiver
	releasedServerStopped                  func(*reporter.RuntimeServer)
	afterRuntimeAttachmentClose            func() error
	afterRuntimeDirectoryCreation          func() error
	afterRuntimeSocketListen               func(*reporter.RuntimeServer) error
	afterRuntimeDirectoryPublish           func() error
	runtimeAttachmentReplayObserved        func()
	runtimeAttachmentReleaseReplayObserved func()
	recoveryErr                            error
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
		store: config.Store, clock: config.Clock, reportSink: sink, newCredential: config.NewCredential,
		newAttentionOperationID: config.NewAttentionOperationID,
		registrations:           make(chan runtimeAttachmentRegistration), releases: make(chan runtimeAttachmentRelease),
		recoveryReady: make(chan struct{}), runDone: make(chan struct{}),
		entries:                   make(map[string]*runtimeAttachmentEntry),
		runtimeAttachmentRefusals: make(map[string]struct{}),
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
	if coordinator.recoveryErr != nil {
		recoveryErr := coordinator.recoveryErr
		coordinator.mu.Unlock()
		return application.PreparedRuntimeAttachment{}, recoveryErr
	}
	if _, refused := coordinator.runtimeAttachmentRefusals[request.TaskHandle]; refused {
		coordinator.mu.Unlock()
		return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: filesystem ownership is unproven")
	}
	if existing := coordinator.entries[request.TaskHandle]; existing != nil {
		if existing.request != request {
			coordinator.mu.Unlock()
			return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: task binding conflicts")
		}
		if existing.state == runtimeAttachmentEntryPending {
			done := existing.registrationDone
			observed := coordinator.runtimeAttachmentReplayObserved
			coordinator.mu.Unlock()
			if observed != nil {
				observed()
			}
			if err := coordinator.waitRuntimeAttachmentReplay(
				ctx, done, "prepare runtime attachment: coordinator stopped",
			); err != nil {
				return application.PreparedRuntimeAttachment{}, err
			}
			coordinator.mu.Lock()
			attachment, resultErr := runtimeAttachmentRegistrationResult(existing)
			coordinator.mu.Unlock()
			return attachment, resultErr
		}
		if existing.state != runtimeAttachmentEntryReady {
			state := existing.state
			coordinator.mu.Unlock()
			return application.PreparedRuntimeAttachment{}, errors.Join(
				errors.New("prepare runtime attachment: task attachment is unavailable"),
				runtimeAttachmentEntryUnavailable(state),
			)
		}
		coordinator.mu.Unlock()
		return existing.attachment, nil
	}
	taskRoot := filepath.Join(coordinator.runtimeRoot, request.TaskHandle)
	attachment := application.PreparedRuntimeAttachment{
		Kind: application.RuntimeAttachmentUnixSocket, SourcePath: filepath.Join(taskRoot, "attachment.sock"),
	}
	if len([]byte(attachment.SourcePath)) > 100 {
		coordinator.mu.Unlock()
		return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: socket path exceeds the Unix bound")
	}
	entry, err := coordinator.listenRuntimeAttachment(request, attachment)
	if err != nil {
		coordinator.mu.Unlock()
		return application.PreparedRuntimeAttachment{}, err
	}
	if err := entry.attachment.Validate(); err != nil {
		coordinator.mu.Unlock()
		return application.PreparedRuntimeAttachment{}, errors.Join(
			errors.New("prepare runtime attachment: relay identity is invalid"), entry.server.Close(),
		)
	}
	entry.state = runtimeAttachmentEntryPending
	entry.registrationDone = make(chan struct{})
	coordinator.entries[request.TaskHandle] = entry
	coordinator.mu.Unlock()
	registrationErr := coordinator.registerRuntimeAttachment(ctx, entry.server)
	if registrationErr != nil {
		registrationErr = errors.Join(registrationErr, coordinator.releaseFailedRuntimeAttachmentRegistration(entry))
	}
	coordinator.completeRuntimeAttachmentRegistration(request.TaskHandle, entry, registrationErr)
	return runtimeAttachmentRegistrationResult(entry)
}

func (coordinator *runtimeAttachmentCoordinator) registerRuntimeAttachment(
	ctx context.Context,
	server *reporter.RuntimeServer,
) error {
	registration := runtimeAttachmentRegistration{server: server, ready: make(chan error, 1)}
	select {
	case coordinator.registrations <- registration:
	case <-ctx.Done():
		return ctx.Err()
	case <-coordinator.runDone:
		return errors.New("prepare runtime attachment: coordinator stopped")
	}
	select {
	case err := <-registration.ready:
		return err
	case <-coordinator.runDone:
		return errors.New("prepare runtime attachment: coordinator stopped")
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
	if entry == nil || entry.state != runtimeAttachmentEntryReady {
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

func (coordinator *runtimeAttachmentCoordinator) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run runtime attachment coordinator: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	defer close(coordinator.runDone)
	results := make(chan runtimeAttachmentResult)
	active := make(map[*reporter.RuntimeServer]struct{})
	releasing := make(map[*reporter.RuntimeServer]struct{})
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
		case release := <-coordinator.releases:
			if _, found := active[release.server]; !found {
				release.ready <- errors.New("runtime attachment server is not active")
				continue
			}
			delete(active, release.server)
			releasing[release.server] = struct{}{}
			release.ready <- nil
		case result := <-results:
			if _, released := releasing[result.server]; released {
				delete(releasing, result.server)
				if coordinator.releasedServerStopped != nil {
					coordinator.releasedServerStopped(result.server)
				}
				if ctx.Err() != nil && len(active) == 0 && len(releasing) == 0 {
					return nil
				}
				continue
			}
			if _, found := active[result.server]; !found {
				return coordinator.stopServers(active, releasing, results,
					errors.New("unregistered runtime attachment server stopped"))
			}
			delete(active, result.server)
			if ctx.Err() == nil {
				return coordinator.stopServers(active, releasing, results,
					errors.Join(errors.New("runtime attachment server stopped"), result.err))
			}
			if len(active) == 0 && len(releasing) == 0 {
				return nil
			}
		case <-ctx.Done():
			return coordinator.stopServers(active, releasing, results, nil)
		}
	}
}

func (coordinator *runtimeAttachmentCoordinator) stopServers(
	active map[*reporter.RuntimeServer]struct{},
	releasing map[*reporter.RuntimeServer]struct{},
	results <-chan runtimeAttachmentResult,
	resultErr error,
) error {
	for server := range active {
		resultErr = errors.Join(resultErr, coordinator.closeRuntimeServerForShutdown(server))
	}
	for remaining := len(active) + len(releasing); remaining > 0; remaining-- {
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
		if entry.server == server && entry.state != runtimeAttachmentEntryReleasing {
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
