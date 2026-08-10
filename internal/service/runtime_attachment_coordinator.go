package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

var attachmentTargetPattern = regexp.MustCompile(`^attachment-[a-f0-9]{32}\.sock$`)

type runtimeAttachmentCoordinatorConfig struct {
	RuntimeRoot   string
	Reports       application.ReportMutationStore
	Clock         application.Clock
	NewCredential func() (string, error)
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
	runtimeRoot   string
	reportSink    *application.ReportSink
	newCredential func() (string, error)
	registrations chan runtimeAttachmentRegistration
	mu            sync.Mutex
	entries       map[string]*runtimeAttachmentEntry
}

func newRuntimeAttachmentCoordinator(config runtimeAttachmentCoordinatorConfig) (*runtimeAttachmentCoordinator, error) {
	if config.Reports == nil || config.Clock == nil || config.NewCredential == nil {
		return nil, errors.New("create runtime attachment coordinator: reports, clock, and credential source are required")
	}
	runtimeRoot, err := ensureOwnedRuntimeRoot(config.RuntimeRoot)
	if err != nil {
		return nil, err
	}
	sink, err := application.NewReportSink(application.ReportSinkConfig{Store: config.Reports, Clock: config.Clock})
	if err != nil {
		return nil, fmt.Errorf("create runtime attachment coordinator report sink: %w", err)
	}
	return &runtimeAttachmentCoordinator{
		runtimeRoot: runtimeRoot, reportSink: sink, newCredential: config.NewCredential,
		registrations: make(chan runtimeAttachmentRegistration), entries: make(map[string]*runtimeAttachmentEntry),
	}, nil
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
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if existing := coordinator.entries[request.TaskHandle]; existing != nil {
		if existing.request != request {
			return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: task binding conflicts")
		}
		return existing.attachment, nil
	}
	taskRoot := filepath.Join(coordinator.runtimeRoot, request.TaskHandle)
	if err := ensureTaskRuntimeDirectory(taskRoot); err != nil {
		return application.PreparedRuntimeAttachment{}, err
	}
	attachment := application.PreparedRuntimeAttachment{
		Kind: application.RuntimeAttachmentUnixSocket, SourcePath: filepath.Join(taskRoot, "attachment.sock"),
	}
	if err := attachment.Validate(); err != nil || len([]byte(attachment.SourcePath)) > 100 {
		return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: socket path exceeds the Unix bound")
	}
	credential, err := coordinator.newCredential()
	if err != nil {
		return application.PreparedRuntimeAttachment{}, errors.New("prepare runtime attachment: credential source failed")
	}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: request.TaskHandle, BriefRevision: request.BriefRevision,
		BriefRevisionHash: request.BriefRevisionHash, Credential: credential, Sink: coordinator.reportSink,
	})
	if err != nil {
		return application.PreparedRuntimeAttachment{}, fmt.Errorf("prepare runtime attachment endpoint: %w", err)
	}
	client, err := reporter.NewClient(endpoint, credential)
	if err != nil {
		return application.PreparedRuntimeAttachment{}, fmt.Errorf("prepare runtime attachment client: %w", err)
	}
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: attachment.SourcePath, Brief: request.Brief, Reporter: client,
	})
	if err != nil {
		return application.PreparedRuntimeAttachment{}, err
	}
	entry := &runtimeAttachmentEntry{request: request, attachment: attachment, server: server}
	coordinator.entries[request.TaskHandle] = entry
	registration := runtimeAttachmentRegistration{server: server, ready: make(chan error, 1)}
	select {
	case coordinator.registrations <- registration:
	case <-ctx.Done():
		delete(coordinator.entries, request.TaskHandle)
		return application.PreparedRuntimeAttachment{}, errors.Join(ctx.Err(), server.Close())
	}
	select {
	case err := <-registration.ready:
		if err != nil {
			delete(coordinator.entries, request.TaskHandle)
			return application.PreparedRuntimeAttachment{}, errors.Join(err, server.Close())
		}
		return attachment, nil
	case <-ctx.Done():
		delete(coordinator.entries, request.TaskHandle)
		return application.PreparedRuntimeAttachment{}, errors.Join(ctx.Err(), server.Close())
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
	if domain.ValidateTaskHandle(request.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("managedRunId", request.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("workspaceLeaseId", request.WorkspaceLeaseID) != nil ||
		domain.ValidateAuthorityReference("executionAttachmentId", request.ExecutionAttachmentID) != nil ||
		domain.ValidateOperationID(request.LaunchOperationID) != nil ||
		!attachmentTargetPattern.MatchString(request.AttachmentTargetName) || request.Acknowledger == nil {
		return errors.New("bind runtime attachment: activation binding is invalid")
	}
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	entry := coordinator.entries[request.TaskHandle]
	if entry == nil {
		return errors.New("bind runtime attachment: prepared socket is unavailable")
	}
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

func (coordinator *runtimeAttachmentCoordinator) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run runtime attachment coordinator: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	results := make(chan runtimeAttachmentResult)
	active := make(map[*reporter.RuntimeServer]struct{})
	for {
		select {
		case registration := <-coordinator.registrations:
			active[registration.server] = struct{}{}
			go func(server *reporter.RuntimeServer) {
				results <- runtimeAttachmentResult{server: server, err: server.Serve(ctx)}
			}(registration.server)
			registration.ready <- nil
		case result := <-results:
			delete(active, result.server)
			if ctx.Err() == nil {
				return coordinator.stopServers(active, results, errors.Join(errors.New("runtime attachment server stopped"), result.err))
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
		resultErr = errors.Join(resultErr, server.Close())
	}
	for range active {
		result := <-results
		if result.err != nil {
			resultErr = errors.Join(resultErr, result.err)
		}
	}
	return resultErr
}

func validateRuntimeAttachmentPreparation(request application.RuntimeAttachmentPreparationRequest) error {
	if domain.ValidateOperationID(request.OperationID) != nil || domain.ValidateTaskHandle(request.TaskHandle) != nil ||
		request.Brief.Validate() != nil || request.BriefRevision != request.Brief.Revision ||
		request.BriefRevisionHash != request.Brief.RevisionHash || !filepath.IsAbs(request.WorkingDirectory) ||
		filepath.Clean(request.WorkingDirectory) != request.WorkingDirectory {
		return errors.New("prepare runtime attachment: task scope is invalid")
	}
	resolved, err := filepath.EvalSymlinks(request.WorkingDirectory)
	if err != nil || resolved != request.WorkingDirectory {
		return errors.New("prepare runtime attachment: workspace is not canonical")
	}
	return nil
}

func ensureOwnedRuntimeRoot(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(os.PathSeparator) || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("create runtime attachment coordinator: runtime root is invalid")
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", errors.New("create runtime attachment coordinator: runtime root is unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("create runtime attachment coordinator: runtime root is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.New("create runtime attachment coordinator: runtime root is not canonical")
	}
	return path, nil
}

func ensureTaskRuntimeDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return errors.New("prepare runtime attachment: task runtime directory is unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("prepare runtime attachment: task runtime directory is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("prepare runtime attachment: task runtime directory is not canonical")
	}
	return nil
}

var _ application.RuntimeAttachmentCoordinator = (*runtimeAttachmentCoordinator)(nil)
