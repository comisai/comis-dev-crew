package reporter

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	runtimeProtocolVersion      = "devcrew.runtime.v1"
	maximumRuntimeRequestBytes  = 18 * 1024
	maximumRuntimeResponseBytes = 128 * 1024
	maximumRuntimePath          = 100
	runtimeDeadline             = 5 * time.Second
)

// RuntimeError is a content-free worker-facing attachment failure.
type RuntimeError struct {
	Code string `json:"code"`
}

// RuntimeOutcome is the closed protected-attachment response envelope.
type RuntimeOutcome struct {
	Version           string                             `json:"version"`
	Brief             *domain.WorkerBrief                `json:"brief,omitempty"`
	Receipt           *domain.ReportReceipt              `json:"receipt,omitempty"`
	Acknowledgement   *application.LaunchAcknowledgement `json:"acknowledgement,omitempty"`
	AttentionResponse *runtimeAttentionOutcome           `json:"attentionResponse,omitempty"`
	Error             *RuntimeError                      `json:"error,omitempty"`
}

type runtimeRequest struct {
	Version         string                             `json:"version"`
	Kind            string                             `json:"kind"`
	Report          *domain.WorkerReport               `json:"report,omitempty"`
	Acknowledgement *application.LaunchAcknowledgement `json:"acknowledgement,omitempty"`
	ExternalKey     string                             `json:"externalKey,omitempty"`
}

// RuntimeServerConfig binds one socket capability to one exact brief and
// already-authenticated in-process reporter client.
type RuntimeServerConfig struct {
	SocketPath              string
	Brief                   domain.WorkerBrief
	Reporter                *Client
	LaunchOperationID       string
	ExpectedLaunch          application.LaunchAcknowledgement
	LaunchAcknowledger      application.WorkerLaunchAcknowledger
	AttentionResponses      AttentionResponseReceiver
	NewAttentionOperationID func() (string, error)
}

// RuntimeLaunchConfig binds activation-returned authority to an already-open
// reporter socket. Identical replays keep the original binding.
type RuntimeLaunchConfig struct {
	OperationID  string
	Expected     application.LaunchAcknowledgement
	Acknowledger application.WorkerLaunchAcknowledger
}

// RuntimeServer serves a single task capability on one owner-only Unix socket.
type RuntimeServer struct {
	listener                *net.UnixListener
	socketPath              string
	socketInfo              os.FileInfo
	brief                   domain.WorkerBrief
	reporter                *Client
	attentionResponses      AttentionResponseReceiver
	newAttentionOperationID func() (string, error)
	launchMu                sync.RWMutex
	launch                  *RuntimeLaunchConfig
	closeOnce               sync.Once
	closeErr                error
	waitGroup               sync.WaitGroup
}

// ListenRuntime creates a new attachment without replacing an existing path.
func ListenRuntime(config RuntimeServerConfig) (*RuntimeServer, error) {
	if err := config.Brief.Validate(); err != nil || config.Reporter == nil {
		return nil, errors.New("listen runtime attachment: pinned brief and reporter are required")
	}
	if err := validateRuntimeLaunchConfig(config); err != nil {
		return nil, err
	}
	if err := validateRuntimeSocketTarget(config.SocketPath); err != nil {
		return nil, err
	}
	address, err := net.ResolveUnixAddr("unix", config.SocketPath)
	if err != nil {
		return nil, errors.New("listen runtime attachment: socket address is invalid")
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return nil, errors.New("listen runtime attachment: socket is unavailable")
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(config.SocketPath, 0o600); err != nil {
		return nil, errors.Join(errors.New("listen runtime attachment: socket mode cannot be secured"), listener.Close(), os.Remove(config.SocketPath))
	}
	info, err := os.Lstat(config.SocketPath)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 {
		return nil, errors.Join(errors.New("listen runtime attachment: socket identity is unavailable"), listener.Close(), os.Remove(config.SocketPath))
	}
	server := &RuntimeServer{
		listener: listener, socketPath: config.SocketPath, socketInfo: info,
		brief: config.Brief, reporter: config.Reporter,
		attentionResponses: config.AttentionResponses, newAttentionOperationID: config.NewAttentionOperationID,
	}
	if config.LaunchOperationID != "" {
		if err := server.BindLaunch(RuntimeLaunchConfig{
			OperationID: config.LaunchOperationID, Expected: config.ExpectedLaunch,
			Acknowledger: config.LaunchAcknowledger,
		}); err != nil {
			return nil, errors.Join(err, server.Close())
		}
	}
	return server, nil
}

// BindLaunch attaches one exact activation identity without replacing the
// socket Comis already validated. Altered replays fail closed.
func (server *RuntimeServer) BindLaunch(config RuntimeLaunchConfig) error {
	if server == nil || server.reporter == nil {
		return errors.New("bind runtime launch: server is unavailable")
	}
	if err := validateRuntimeLaunchBinding(server.brief, server.reporter, config); err != nil {
		return err
	}
	server.launchMu.Lock()
	defer server.launchMu.Unlock()
	if server.launch != nil {
		if server.launch.OperationID != config.OperationID || server.launch.Expected != config.Expected {
			return errors.New("bind runtime launch: activation binding conflicts")
		}
		return nil
	}
	binding := config
	server.launch = &binding
	return nil
}

// Serve accepts bounded one-request connections until cancellation or Close.
func (server *RuntimeServer) Serve(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errors.New("serve runtime attachment: context is required")
	}
	if server == nil || server.listener == nil {
		return errors.New("serve runtime attachment: server is unavailable")
	}
	stopCancellation := make(chan struct{})
	cancellationDone := make(chan struct{})
	go func() {
		defer close(cancellationDone)
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-stopCancellation:
		}
	}()
	defer func() {
		close(stopCancellation)
		<-cancellationDone
		server.waitGroup.Wait()
	}()
	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return errors.New("serve runtime attachment: accept failed")
		}
		server.waitGroup.Add(1)
		go func() {
			defer server.waitGroup.Done()
			_ = server.serveConnection(ctx, connection)
		}()
	}
}

// Close stops the exact listener and removes only the same socket inode.
func (server *RuntimeServer) Close() error {
	if server == nil || server.listener == nil {
		return nil
	}
	server.closeOnce.Do(func() {
		var closeErr error
		if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = errors.New("close runtime attachment: listener close failed")
		}
		server.closeErr = errors.Join(closeErr, removeRuntimeSocket(server.socketPath, server.socketInfo))
	})
	return server.closeErr
}

func (server *RuntimeServer) serveConnection(ctx context.Context, connection *net.UnixConn) error {
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(runtimeDeadline)); err != nil {
		return errors.New("serve runtime attachment: connection deadline failed")
	}
	reader := bufio.NewReaderSize(connection, maximumRuntimeRequestBytes+1)
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > maximumRuntimeRequestBytes {
		return writeRuntimeOutcome(connection, runtimeRejected("request_too_large"))
	}
	if err != nil || len(line) < 2 {
		return writeRuntimeOutcome(connection, runtimeRejected("malformed_request"))
	}
	line = line[:len(line)-1]
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	var request runtimeRequest
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Version != runtimeProtocolVersion {
		return writeRuntimeOutcome(connection, runtimeRejected("malformed_request"))
	}
	var outcome RuntimeOutcome
	switch request.Kind {
	case "brief":
		if request.Report != nil || request.Acknowledgement != nil || request.ExternalKey != "" {
			outcome = runtimeRejected("malformed_request")
		} else {
			brief := server.brief
			outcome = RuntimeOutcome{Version: runtimeProtocolVersion, Brief: &brief}
		}
	case "report":
		if request.Report == nil || request.Acknowledgement != nil || request.ExternalKey != "" {
			outcome = runtimeRejected("malformed_request")
		} else if receipt, err := server.reporter.Report(ctx, *request.Report); err != nil {
			outcome = runtimeRejected("report_rejected")
		} else {
			outcome = RuntimeOutcome{Version: runtimeProtocolVersion, Receipt: &receipt}
		}
	case "launch":
		launch := server.launchBinding()
		if request.Report != nil || request.Acknowledgement != nil || request.ExternalKey != "" {
			outcome = runtimeRejected("malformed_request")
		} else if launch == nil {
			outcome = runtimeRejected("launch_unavailable")
		} else {
			expected := launch.Expected
			outcome = RuntimeOutcome{Version: runtimeProtocolVersion, Acknowledgement: &expected}
		}
	case "acknowledge":
		outcome = server.acknowledgeLaunch(ctx, request, server.launchBinding())
	case "attention_response":
		outcome = server.receiveAttentionResponse(ctx, request, server.launchBinding())
	default:
		outcome = runtimeRejected("unknown_request")
	}
	return writeRuntimeOutcome(connection, outcome)
}

func (server *RuntimeServer) acknowledgeLaunch(ctx context.Context, request runtimeRequest, launch *RuntimeLaunchConfig) RuntimeOutcome {
	if request.Report != nil || request.Acknowledgement == nil || request.ExternalKey != "" || launch == nil ||
		*request.Acknowledgement != launch.Expected {
		return runtimeRejected("acknowledgement_rejected")
	}
	result, err := launch.Acknowledger.AcknowledgeWorkerLaunch(ctx, application.AcknowledgeWorkerLaunchCommand{
		OperationID: launch.OperationID, Acknowledgement: launch.Expected,
	})
	if err != nil || !validLaunchAcknowledgementResult(result, launch.OperationID, launch.Expected) {
		return runtimeRejected("acknowledgement_rejected")
	}
	acknowledged := launch.Expected
	return RuntimeOutcome{Version: runtimeProtocolVersion, Acknowledgement: &acknowledged}
}

// RuntimeClient is the worker-side narrow brief/read and report/append client.
type RuntimeClient struct {
	socketPath     string
	socketInfo     os.FileInfo
	mountDirectory string
	mountInfo      os.FileInfo
	timeout        time.Duration
}

// Brief fetches the exact pinned task brief from the socket capability.
func (client *RuntimeClient) Brief(ctx context.Context) (domain.WorkerBrief, error) {
	outcome, err := client.call(ctx, runtimeRequest{Version: runtimeProtocolVersion, Kind: "brief"})
	if err != nil {
		return domain.WorkerBrief{}, err
	}
	if outcome.Error != nil || outcome.Brief == nil || outcome.Receipt != nil || outcome.Acknowledgement != nil ||
		outcome.AttentionResponse != nil {
		return domain.WorkerBrief{}, errors.New("read runtime brief: attachment rejected the request")
	}
	if err := outcome.Brief.Validate(); err != nil {
		return domain.WorkerBrief{}, errors.New("read runtime brief: attachment returned an invalid brief")
	}
	return *outcome.Brief, nil
}

// Report appends one sparse report without accepting any task identity input.
func (client *RuntimeClient) Report(ctx context.Context, report domain.WorkerReport) (domain.ReportReceipt, error) {
	outcome, err := client.call(ctx, runtimeRequest{Version: runtimeProtocolVersion, Kind: "report", Report: &report})
	if err != nil {
		return domain.ReportReceipt{}, err
	}
	if outcome.Error != nil || outcome.Receipt == nil || outcome.Brief != nil || outcome.Acknowledgement != nil ||
		outcome.AttentionResponse != nil {
		return domain.ReportReceipt{}, errors.New("submit runtime report: attachment rejected the request")
	}
	if err := domain.ValidateTaskHandle(outcome.Receipt.TaskHandle); err != nil ||
		outcome.Receipt.LocalReportID != report.LocalReportID || outcome.Receipt.StateVersion < 1 ||
		outcome.Receipt.AcceptedAt.IsZero() || outcome.Receipt.AcceptedAt.Location() != time.UTC {
		return domain.ReportReceipt{}, errors.New("submit runtime report: attachment returned an invalid receipt")
	}
	return *outcome.Receipt, nil
}

// Acknowledge verifies the protected launch identity, current canonical cwd,
// and pinned brief before echoing the complete launch fact to the service.
func (client *RuntimeClient) Acknowledge(ctx context.Context, workingDirectory string) error {
	resolved, err := filepath.EvalSymlinks(workingDirectory)
	if err != nil || !filepath.IsAbs(workingDirectory) || filepath.Clean(workingDirectory) != workingDirectory || resolved != workingDirectory {
		return errors.New("acknowledge runtime launch: working directory is invalid")
	}
	launch, err := client.launchAcknowledgement(ctx)
	if err != nil {
		return err
	}
	brief, err := client.Brief(ctx)
	if err != nil || launch.WorkingDirectory != workingDirectory ||
		launch.BriefRevision != brief.Revision || launch.BriefRevisionHash != brief.RevisionHash {
		return errors.New("acknowledge runtime launch: protected binding differs")
	}
	outcome, err := client.call(ctx, runtimeRequest{
		Version: runtimeProtocolVersion, Kind: "acknowledge", Acknowledgement: &launch,
	})
	if err != nil {
		return err
	}
	if outcome.Error != nil || outcome.Acknowledgement == nil || outcome.Brief != nil || outcome.Receipt != nil ||
		outcome.AttentionResponse != nil ||
		*outcome.Acknowledgement != launch {
		return errors.New("acknowledge runtime launch: attachment rejected the operation")
	}
	return nil
}

func (client *RuntimeClient) launchAcknowledgement(ctx context.Context) (application.LaunchAcknowledgement, error) {
	outcome, err := client.call(ctx, runtimeRequest{Version: runtimeProtocolVersion, Kind: "launch"})
	if err != nil {
		return application.LaunchAcknowledgement{}, err
	}
	if outcome.Error != nil || outcome.Acknowledgement == nil || outcome.Brief != nil || outcome.Receipt != nil ||
		outcome.AttentionResponse != nil ||
		outcome.Acknowledgement.Validate() != nil {
		return application.LaunchAcknowledgement{}, errors.New("read runtime launch: attachment returned an invalid binding")
	}
	return *outcome.Acknowledgement, nil
}

func (client *RuntimeClient) call(ctx context.Context, request runtimeRequest) (RuntimeOutcome, error) {
	if ctx == nil {
		return RuntimeOutcome{}, errors.New("call runtime attachment: context is required")
	}
	if err := ctx.Err(); err != nil {
		return RuntimeOutcome{}, err
	}
	if client == nil || client.socketInfo == nil {
		return RuntimeOutcome{}, errors.New("call runtime attachment: client is unavailable")
	}
	if client.mountInfo != nil {
		currentMount, mountErr := os.Lstat(client.mountDirectory)
		if mountErr != nil || !os.SameFile(client.mountInfo, currentMount) || !currentMount.IsDir() ||
			currentMount.Mode()&os.ModeSymlink != 0 || !safeRuntimeMountPermissions(currentMount.Mode()) {
			return RuntimeOutcome{}, errors.New("call runtime attachment: protected mount identity changed")
		}
	}
	current, err := os.Lstat(client.socketPath)
	if err != nil || !os.SameFile(client.socketInfo, current) {
		return RuntimeOutcome{}, errors.New("call runtime attachment: socket identity changed")
	}
	connection, err := net.DialTimeout("unix", client.socketPath, client.timeout)
	if err != nil {
		return RuntimeOutcome{}, errors.New("call runtime attachment: socket is unavailable")
	}
	defer connection.Close()
	deadline := time.Now().Add(client.timeout)
	if contextDeadline, found := ctx.Deadline(); found && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := connection.SetDeadline(deadline); err != nil {
		return RuntimeOutcome{}, errors.New("call runtime attachment: deadline failed")
	}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		return RuntimeOutcome{}, errors.New("call runtime attachment: request write failed")
	}
	reader := bufio.NewReaderSize(connection, maximumRuntimeResponseBytes+1)
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > maximumRuntimeResponseBytes || err != nil || len(line) < 2 {
		return RuntimeOutcome{}, errors.New("call runtime attachment: response is invalid")
	}
	line = line[:len(line)-1]
	decoder := json.NewDecoder(strings.NewReader(string(line)))
	decoder.DisallowUnknownFields()
	var outcome RuntimeOutcome
	if err := decoder.Decode(&outcome); err != nil || decoder.Decode(&struct{}{}) != io.EOF || outcome.Version != runtimeProtocolVersion {
		return RuntimeOutcome{}, errors.New("call runtime attachment: response is invalid")
	}
	return outcome, nil
}

func writeRuntimeOutcome(connection net.Conn, outcome RuntimeOutcome) error {
	if err := json.NewEncoder(connection).Encode(outcome); err != nil {
		return errors.New("serve runtime attachment: response write failed")
	}
	return nil
}

func runtimeRejected(code string) RuntimeOutcome {
	return RuntimeOutcome{Version: runtimeProtocolVersion, Error: &RuntimeError{Code: code}}
}

func validLaunchAcknowledgementResult(
	result application.MutationResult,
	operationID string,
	expected application.LaunchAcknowledgement,
) bool {
	return result.Operation.ID == operationID && result.Operation.Status == domain.OperationCompleted &&
		(result.Task.State == domain.TaskLaunching || result.Task.State == domain.TaskWorking) &&
		result.Task.Handle == expected.TaskHandle && result.Task.ManagedRunID == expected.ManagedRunID &&
		result.Task.WorkspaceLeaseID == expected.WorkspaceLeaseID && result.Task.BriefRevision == expected.BriefRevision &&
		result.Task.BriefRevisionHash == expected.BriefRevisionHash
}

func validateRuntimeSocketTarget(path string) error {
	if err := validateRuntimeSourceSocketPath(path); err != nil {
		return err
	}
	parent := filepath.Dir(path)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("listen runtime attachment: parent directory is unavailable or unsafe")
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil || resolved != parent {
		return errors.New("listen runtime attachment: parent directory is not canonical")
	}
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return errors.New("listen runtime attachment: socket target already exists or is ambiguous")
	}
	return nil
}

func removeRuntimeSocket(path string, original os.FileInfo) error {
	current, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || original == nil || !os.SameFile(original, current) || current.Mode()&os.ModeSocket == 0 {
		return errors.New("close runtime attachment: socket identity is ambiguous; path preserved")
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("close runtime attachment: remove socket: %w", err)
	}
	return nil
}
