package comiswire

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// ControlHandler consumes authenticated host-to-service lifecycle requests.
// Implementations must durably deduplicate every operation ID.
type ControlHandler interface {
	Activate(context.Context, ActivateRequestParams) (ActivateResponseResult, error)
	Abandon(context.Context, AbandonRequestParams) (AbandonResponseResult, error)
	TerminalEvent(context.Context, TerminalEventRequestParams) (TerminalEventResponseResult, error)
}

// ControlConnectionConfig identifies one installed capability-service instance.
type ControlConnectionConfig struct {
	SocketPath           string
	Credential           string
	ServiceInstanceID    ServiceInstanceID
	HandshakeOperationID OperationID
	Handler              ControlHandler
	RequestTimeout       time.Duration
	MinimumBackoff       time.Duration
	MaximumBackoff       time.Duration
}

// ControlConnection maintains the single authenticated bidirectional Comis
// connection. Report retries remain the durable outbox owner's responsibility.
type ControlConnection struct {
	config ControlConnectionConfig

	mu      sync.Mutex
	session *controlSession
	changed chan struct{}
}

// NewControlConnection validates the exact pinned handshake before any socket
// is opened. Run owns reconnect supervision and must be joined by its caller.
func NewControlConnection(config ControlConnectionConfig) (*ControlConnection, error) {
	if config.Handler == nil {
		return nil, errors.New("create Comis control connection: handler is required")
	}
	if config.RequestTimeout <= 0 {
		return nil, errors.New("create Comis control connection: request timeout must be positive")
	}
	if config.MinimumBackoff <= 0 || config.MaximumBackoff < config.MinimumBackoff {
		return nil, errors.New("create Comis control connection: reconnect backoff is invalid")
	}
	if _, err := NewAuthenticatedUnixClient(config.SocketPath, config.Credential, config.RequestTimeout); err != nil {
		return nil, err
	}
	request := controlHandshake(config)
	if err := validateGeneratedDocument(schemaHandshakeRequest, request); err != nil {
		return nil, fmt.Errorf("create Comis control connection: invalid handshake: %w", err)
	}
	return &ControlConnection{config: config, changed: make(chan struct{})}, nil
}

// Run reconnects with bounded exponential backoff until cancellation. A lost
// session never retries an in-flight report inside the transport.
func (connection *ControlConnection) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("run Comis control connection: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backoff := connection.config.MinimumBackoff
	for {
		session, err := connection.connect(ctx)
		if err == nil {
			connection.publish(session)
			_ = session.serve(ctx)
			connection.unpublish(session)
			_ = session.close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := waitControlBackoff(ctx, backoff); err != nil {
			return err
		}
		if backoff < connection.config.MaximumBackoff {
			backoff *= 2
			if backoff > connection.config.MaximumBackoff {
				backoff = connection.config.MaximumBackoff
			}
		}
	}
}

// Report sends one already-durable report on the current authenticated
// connection. An error is uncertain and must be reconciled by stable IDs.
func (connection *ControlConnection) Report(ctx context.Context, params ReportRequestParams) (ReportResponseResult, error) {
	if ctx == nil {
		return ReportResponseResult{}, errors.New("report to Comis: context is required")
	}
	request := ReportRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsReport, Params: params}
	if err := validateGeneratedDocument(schemaReportRequest, request); err != nil {
		return ReportResponseResult{}, fmt.Errorf("report to Comis: invalid request: %w", err)
	}
	session, err := connection.awaitSession(ctx)
	if err != nil {
		return ReportResponseResult{}, err
	}
	var response ReportResponse
	authenticated := authenticatedReportRequest{ReportRequest: request, Bearer: connection.config.Credential}
	if err := session.call(ctx, authenticated, params.OperationID, &response); err != nil {
		return ReportResponseResult{}, fmt.Errorf("report to Comis: outcome uncertain: %w", err)
	}
	if response.Result.ManagedRunID != params.ManagedRunID || response.Result.ServiceReportID != params.ServiceReportID {
		return ReportResponseResult{}, errors.New("report to Comis: acknowledgement identity differs")
	}
	return response.Result, nil
}

// PutEvidence sends one already-durable immutable evidence body on the current
// authenticated connection. An error leaves the host outcome uncertain.
func (connection *ControlConnection) PutEvidence(
	ctx context.Context,
	params PutEvidenceRequestParams,
) (PutEvidenceResponseResult, error) {
	if ctx == nil {
		return PutEvidenceResponseResult{}, errors.New("put evidence to Comis: context is required")
	}
	body, err := base64.StdEncoding.DecodeString(params.BodyBase64)
	if err != nil || len(body) == 0 || len(body) > MaxEvidenceBytes {
		return PutEvidenceResponseResult{}, errors.New("put evidence to Comis: body is invalid")
	}
	if fmt.Sprintf("%x", sha256.Sum256(body)) != params.ContentHash {
		return PutEvidenceResponseResult{}, errors.New("put evidence to Comis: body hash differs")
	}
	if params.VerificationLevel == EvidenceVerificationLevelHostVerified {
		return PutEvidenceResponseResult{}, errors.New("put evidence to Comis: host verification is reserved")
	}
	request := PutEvidenceRequest{
		JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsPutEvidence, Params: params,
	}
	if err := validateGeneratedDocument(schemaPutEvidenceRequest, request); err != nil {
		return PutEvidenceResponseResult{}, fmt.Errorf("put evidence to Comis: invalid request: %w", err)
	}
	session, err := connection.awaitSession(ctx)
	if err != nil {
		return PutEvidenceResponseResult{}, err
	}
	var response PutEvidenceResponse
	authenticated := authenticatedPutEvidenceRequest{PutEvidenceRequest: request, Bearer: connection.config.Credential}
	if err := session.call(ctx, authenticated, params.OperationID, &response); err != nil {
		return PutEvidenceResponseResult{}, fmt.Errorf("put evidence to Comis: outcome uncertain: %w", err)
	}
	if err := validateGeneratedDocument(schemaPutEvidenceResponse, response); err != nil {
		return PutEvidenceResponseResult{}, fmt.Errorf("put evidence to Comis: invalid response: %w", err)
	}
	if response.Result.ManagedRunID != params.ManagedRunID || response.Result.EvidenceRef != params.EvidenceRef ||
		response.Result.ContentHash != params.ContentHash || response.Result.VerificationLevel != params.VerificationLevel {
		return PutEvidenceResponseResult{}, errors.New("put evidence to Comis: acknowledgement identity differs")
	}
	return response.Result, nil
}

// Release asks Comis to revoke exact run capabilities and release its lease.
// An error leaves cleanup held until the stable request is reconciled.
func (connection *ControlConnection) Release(
	ctx context.Context,
	params ReleaseRequestParams,
) (ReleaseResponseResult, error) {
	if ctx == nil {
		return ReleaseResponseResult{}, errors.New("release managed run in Comis: context is required")
	}
	request := ReleaseRequest{
		JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsRelease, Params: params,
	}
	if err := validateGeneratedDocument(schemaReleaseRequest, request); err != nil {
		return ReleaseResponseResult{}, fmt.Errorf("release managed run in Comis: invalid request: %w", err)
	}
	session, err := connection.awaitSession(ctx)
	if err != nil {
		return ReleaseResponseResult{}, err
	}
	var response ReleaseResponse
	authenticated := authenticatedReleaseRequest{ReleaseRequest: request, Bearer: connection.config.Credential}
	if err := session.call(ctx, authenticated, params.OperationID, &response); err != nil {
		return ReleaseResponseResult{}, fmt.Errorf("release managed run in Comis: outcome uncertain: %w", err)
	}
	if err := validateGeneratedDocument(schemaReleaseResponse, response); err != nil {
		return ReleaseResponseResult{}, fmt.Errorf("release managed run in Comis: invalid response: %w", err)
	}
	if response.Result.ManagedRunID != params.ManagedRunID ||
		response.Result.WorkspaceLeaseID != params.WorkspaceLeaseID ||
		response.Result.Disposition != params.Disposition ||
		response.Result.ReleasedAtMs != params.ReleasedAtMs ||
		response.Result.State != ManagedRunState("released") {
		return ReleaseResponseResult{}, errors.New("release managed run in Comis: acknowledgement identity differs")
	}
	return response.Result, nil
}

func (connection *ControlConnection) connect(ctx context.Context) (*controlSession, error) {
	before, err := inspectOwnerOnlySocket(connection.config.SocketPath)
	if err != nil {
		return nil, err
	}
	callContext, cancel := context.WithTimeout(ctx, connection.config.RequestTimeout)
	defer cancel()
	socket, err := (&net.Dialer{}).DialContext(callContext, "unix", connection.config.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("connect to Comis control socket: %w", err)
	}
	after, err := inspectOwnerOnlySocket(connection.config.SocketPath)
	if err != nil || !os.SameFile(before, after) {
		_ = socket.Close()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("comis control socket identity changed during connection")
	}
	session := newControlSession(socket, connection.config.Credential, connection.config.Handler, connection.config.RequestTimeout)
	if err := session.handshake(callContext, controlHandshake(connection.config)); err != nil {
		_ = session.close()
		return nil, err
	}
	return session, nil
}

func controlHandshake(config ControlConnectionConfig) HandshakeRequest {
	return HandshakeRequest{
		JSONRPC: JSONRPCVersion, ID: config.HandshakeOperationID, Method: MethodCapabilityServicesHandshake,
		Params: HandshakeRequestParams{
			ProtocolID: ProtocolID, BundleDigest: BundleDigest,
			OperationID: config.HandshakeOperationID, ServiceInstanceID: config.ServiceInstanceID,
			RequestedScopes: requiredControlScopes(),
		},
	}
}

func (connection *ControlConnection) publish(session *controlSession) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	connection.session = session
	close(connection.changed)
	connection.changed = make(chan struct{})
}

func (connection *ControlConnection) unpublish(session *controlSession) {
	connection.mu.Lock()
	defer connection.mu.Unlock()
	if connection.session != session {
		return
	}
	connection.session = nil
	close(connection.changed)
	connection.changed = make(chan struct{})
}

func (connection *ControlConnection) awaitSession(ctx context.Context) (*controlSession, error) {
	for {
		connection.mu.Lock()
		session := connection.session
		changed := connection.changed
		connection.mu.Unlock()
		if session != nil {
			return session, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func waitControlBackoff(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
