package comiswire

import (
	"bufio"
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

type authenticatedActivateRequest struct {
	ActivateRequest
	Bearer string `json:"bearer"`
}

type authenticatedAbandonRequest struct {
	AbandonRequest
	Bearer string `json:"bearer"`
}

type authenticatedTerminalEventRequest struct {
	TerminalEventRequest
	Bearer string `json:"bearer"`
}

type controlFrameHeader struct {
	Error   json.RawMessage `json:"error"`
	ID      json.RawMessage `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
	Method  *Method         `json:"method"`
	Result  json.RawMessage `json:"result"`
}

type controlCallResult struct {
	frame []byte
	err   error
}

type controlSession struct {
	connection net.Conn
	reader     *bufio.Reader
	bearer     string
	handler    ControlHandler
	timeout    time.Duration

	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[OperationID]chan controlCallResult
	done    chan struct{}
	err     error
	once    sync.Once
	workers sync.WaitGroup
	limit   chan struct{}
}

func newControlSession(connection net.Conn, bearer string, handler ControlHandler, timeout time.Duration) *controlSession {
	return &controlSession{
		connection: connection, reader: bufio.NewReaderSize(connection, MaxLineBytes+1),
		bearer: bearer, handler: handler, timeout: timeout,
		pending: make(map[OperationID]chan controlCallResult), done: make(chan struct{}),
		limit: make(chan struct{}, MaxInFlightRequests),
	}
}

func (session *controlSession) handshake(ctx context.Context, request HandshakeRequest) error {
	if deadline, ok := ctx.Deadline(); ok {
		if err := session.connection.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set Comis handshake deadline: %w", err)
		}
	}
	if err := session.write(authenticatedHandshakeRequest{HandshakeRequest: request, Bearer: session.bearer}); err != nil {
		return fmt.Errorf("write Comis handshake: %w", err)
	}
	line, err := readControlLine(session.reader)
	if err != nil {
		return fmt.Errorf("read Comis handshake: %w", err)
	}
	var response HandshakeResponse
	if err := decodeWireResponse(line, request.ID, &response); err != nil {
		return err
	}
	if err := ValidatePayload(PayloadHandshakeResponse, line); err != nil {
		return err
	}
	if response.Result.ProtocolID != ProtocolID || response.Result.BundleDigest != BundleDigest ||
		response.Result.ServiceInstanceID != request.Params.ServiceInstanceID ||
		!sameControlScopes(response.Result.ActiveScopes) {
		return errors.New("comis handshake authority differs")
	}
	if err := session.connection.SetDeadline(time.Time{}); err != nil {
		return fmt.Errorf("clear Comis handshake deadline: %w", err)
	}
	return nil
}

func (session *controlSession) serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = session.close() })
	defer stop()
	for {
		line, err := readControlLine(session.reader)
		if err != nil {
			session.fail(err)
			session.workers.Wait()
			return err
		}
		if err := session.route(ctx, line); err != nil {
			session.fail(err)
			session.workers.Wait()
			return err
		}
	}
}

func (session *controlSession) route(ctx context.Context, line []byte) error {
	if err := rejectDuplicateJSONNames(line); err != nil {
		return fmt.Errorf("inspect Comis control frame: %w", err)
	}
	var header controlFrameHeader
	if err := json.Unmarshal(line, &header); err != nil || header.JSONRPC != JSONRPCVersion {
		return errors.New("comis control frame envelope is invalid")
	}
	if header.Method == nil {
		return session.resolveResponse(header, line)
	}
	select {
	case session.limit <- struct{}{}:
		request := append([]byte(nil), line...)
		session.workers.Add(1)
		go func() {
			defer session.workers.Done()
			defer func() { <-session.limit }()
			requestContext, cancel := context.WithTimeout(ctx, session.timeout)
			defer cancel()
			if err := session.dispatch(requestContext, *header.Method, request); err != nil {
				session.fail(err)
			}
		}()
		return nil
	default:
		return errors.New("comis control request limit exceeded")
	}
}

func (session *controlSession) resolveResponse(header controlFrameHeader, line []byte) error {
	decoded, err := decodeStrictValue(header.ID)
	if err != nil {
		return errors.New("comis control response ID is invalid")
	}
	id, ok := decoded.(string)
	if !ok {
		return errors.New("comis control response ID is absent")
	}
	session.mu.Lock()
	waiter := session.pending[OperationID(id)]
	if waiter != nil {
		delete(session.pending, OperationID(id))
	}
	session.mu.Unlock()
	if waiter == nil {
		return errors.New("comis control response has no pending operation")
	}
	waiter <- controlCallResult{frame: append([]byte(nil), line...)}
	return nil
}

func (session *controlSession) dispatch(ctx context.Context, method Method, line []byte) error {
	switch method {
	case MethodManagedRunsActivate:
		var authenticated authenticatedActivateRequest
		if err := decodeStrictObject(line, &authenticated); err != nil {
			return session.writeFailure(nil, wireFailure(ErrorKindInvalidRequest, "invalid activation envelope"))
		}
		id := authenticated.ID
		if !session.authenticated(authenticated.Bearer) {
			return session.writeFailure(&id, wireFailure(ErrorKindUnauthorizedInstance, "instance credential differs"))
		}
		if authenticated.ID != authenticated.Params.OperationID {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidRequest, "activation operation identity differs"))
		}
		if err := validateBaseRequest(authenticated.ActivateRequest); err != nil {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidParams, "invalid activation request"))
		}
		result, err := session.handler.Activate(ctx, authenticated.Params)
		if err != nil {
			return session.writeFailure(&id, handlerWireFailure(err))
		}
		return session.writeValidated(PayloadActivateResponse, ActivateResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result})
	case MethodManagedRunsAbandon:
		var authenticated authenticatedAbandonRequest
		if err := decodeStrictObject(line, &authenticated); err != nil {
			return session.writeFailure(nil, wireFailure(ErrorKindInvalidRequest, "invalid abandon envelope"))
		}
		id := authenticated.ID
		if !session.authenticated(authenticated.Bearer) {
			return session.writeFailure(&id, wireFailure(ErrorKindUnauthorizedInstance, "instance credential differs"))
		}
		if authenticated.ID != authenticated.Params.OperationID {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidRequest, "abandon operation identity differs"))
		}
		if err := validateBaseRequest(authenticated.AbandonRequest); err != nil {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidParams, "invalid abandon request"))
		}
		result, err := session.handler.Abandon(ctx, authenticated.Params)
		if err != nil {
			return session.writeFailure(&id, handlerWireFailure(err))
		}
		return session.writeValidated(PayloadAbandonResponse, AbandonResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result})
	case MethodManagedRunsCancel:
		var authenticated authenticatedCancelRequest
		if err := decodeStrictObject(line, &authenticated); err != nil {
			return session.writeFailure(nil, wireFailure(ErrorKindInvalidRequest, "invalid cancel envelope"))
		}
		id := authenticated.ID
		if !session.authenticated(authenticated.Bearer) {
			return session.writeFailure(&id, wireFailure(ErrorKindUnauthorizedInstance, "instance credential differs"))
		}
		if authenticated.ID != authenticated.Params.OperationID {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidRequest, "cancel operation identity differs"))
		}
		if err := validateBaseRequest(authenticated.CancelRequest); err != nil {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidParams, "invalid cancel request"))
		}
		result, err := session.handler.Cancel(ctx, authenticated.Params)
		if err != nil {
			return session.writeFailure(&id, handlerWireFailure(err))
		}
		return session.writeValidated(PayloadCancelResponse, CancelResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result})
	case MethodManagedRunsTerminalEvent:
		var authenticated authenticatedTerminalEventRequest
		if err := decodeStrictObject(line, &authenticated); err != nil {
			return session.writeFailure(nil, wireFailure(ErrorKindInvalidRequest, "invalid terminal event envelope"))
		}
		id := authenticated.ID
		if !session.authenticated(authenticated.Bearer) {
			return session.writeFailure(&id, wireFailure(ErrorKindUnauthorizedInstance, "instance credential differs"))
		}
		if authenticated.ID != authenticated.Params.OperationID {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidRequest, "terminal event operation identity differs"))
		}
		if err := validateBaseRequest(authenticated.TerminalEventRequest); err != nil {
			return session.writeFailure(&id, wireFailure(ErrorKindInvalidParams, "invalid terminal event request"))
		}
		result, err := session.handler.TerminalEvent(ctx, authenticated.Params)
		if err != nil {
			return session.writeFailure(&id, handlerWireFailure(err))
		}
		return session.writeValidated(PayloadTerminalEventResponse, TerminalEventResponse{JSONRPC: JSONRPCVersion, ID: id, Result: result})
	default:
		return session.writeFailure(nil, wireFailure(ErrorKindMethodNotFound, "control method is not allowed"))
	}
}

func (session *controlSession) call(ctx context.Context, request any, id OperationID, response any) error {
	callContext, cancel := context.WithTimeout(ctx, session.timeout)
	defer cancel()
	waiter := make(chan controlCallResult, 1)
	session.mu.Lock()
	if _, exists := session.pending[id]; exists {
		session.mu.Unlock()
		return errors.New("comis control operation is already in flight")
	}
	session.pending[id] = waiter
	session.mu.Unlock()
	if err := session.writeWithContext(callContext, request); err != nil {
		session.removePending(id)
		return err
	}
	select {
	case result := <-waiter:
		return resolveControlCall(result, id, response)
	case <-callContext.Done():
		session.removePending(id)
		return resolveDeliveredControlCall(waiter, id, response, callContext.Err())
	case <-session.done:
		session.removePending(id)
		return resolveDeliveredControlCall(waiter, id, response, session.failure())
	}
}

// resolveDeliveredControlCall prefers a response that was already routed for this operation. A
// delivered response is a known outcome, so an end observed in the same instant never replaces it.
func resolveDeliveredControlCall(
	waiter <-chan controlCallResult,
	id OperationID,
	response any,
	ended error,
) error {
	select {
	case result := <-waiter:
		return resolveControlCall(result, id, response)
	default:
		return ended
	}
}

func resolveControlCall(result controlCallResult, id OperationID, response any) error {
	if result.err != nil {
		return result.err
	}
	return decodeWireResponse(result.frame, id, response)
}

func (session *controlSession) writeValidated(target PayloadTarget, response any) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return err
	}
	if err := ValidatePayload(target, encoded); err != nil {
		return fmt.Errorf("validate Comis control response: %w", err)
	}
	return session.write(response)
}

func (session *controlSession) writeFailure(id *OperationID, failure RPCError) error {
	response := ErrorResponse{JSONRPC: JSONRPCVersion, ID: id, Error: failure}
	return session.writeValidated(PayloadErrorResponse, response)
}

func (session *controlSession) writeWithContext(ctx context.Context, request any) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	if deadline, ok := ctx.Deadline(); ok {
		if err := session.connection.SetWriteDeadline(deadline); err != nil {
			return err
		}
		defer session.connection.SetWriteDeadline(time.Time{}) //nolint:errcheck
	}
	return session.writeUnlocked(request)
}

func (session *controlSession) write(value any) error {
	session.writeMu.Lock()
	defer session.writeMu.Unlock()
	return session.writeUnlocked(value)
}

func (session *controlSession) writeUnlocked(value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(encoded) > MaxLineBytes-1 {
		return errors.New("comis control frame exceeds line limit")
	}
	encoded = append(encoded, '\n')
	_, err = io.Copy(session.connection, bytes.NewReader(encoded))
	return err
}

func (session *controlSession) authenticated(presented string) bool {
	return subtle.ConstantTimeCompare([]byte(presented), []byte(session.bearer)) == 1
}

func (session *controlSession) removePending(id OperationID) {
	session.mu.Lock()
	delete(session.pending, id)
	session.mu.Unlock()
}

func (session *controlSession) fail(err error) {
	session.once.Do(func() {
		session.mu.Lock()
		session.err = err
		waiters := session.pending
		session.pending = make(map[OperationID]chan controlCallResult)
		close(session.done)
		session.mu.Unlock()
		_ = session.connection.Close()
		for _, waiter := range waiters {
			waiter <- controlCallResult{err: err}
		}
	})
}

func (session *controlSession) close() error {
	session.fail(net.ErrClosed)
	return nil
}

func (session *controlSession) failure() error {
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.err
}

func validateBaseRequest(request any) error {
	encoded, err := json.Marshal(request)
	if err != nil {
		return err
	}
	return ValidatePayload(PayloadRequest, encoded)
}

func sameControlScopes(scopes []ServiceScope) bool {
	return sameServiceScopeSet(requiredControlScopes(), scopes)
}

func requiredControlScopes() []ServiceScope {
	return []ServiceScope{
		ServiceScopeHealth,
		ServiceScopeAttentionResponse,
		ServiceScopeEvidence,
		ServiceScopeReport,
		ServiceScopeWorkspaceLease,
		ServiceScopeTerminalEvents,
		ServiceScopeExecutionAttachment,
	}
}

func readControlLine(reader *bufio.Reader) ([]byte, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > MaxLineBytes {
		return nil, errors.New("comis control frame exceeds line limit")
	}
	if err != nil {
		return nil, err
	}
	return line[:len(line)-1], nil
}

func handlerWireFailure(err error) RPCError {
	var failure RPCError
	if errors.As(err, &failure) && failure.Kind.Valid() {
		message := failure.Message
		if message == "" || len(message) > 1024 {
			message = "control request rejected"
		}
		return wireFailure(failure.Kind, message)
	}
	return wireFailure(ErrorKindInternalError, "control handler failed")
}

func wireFailure(kind ErrorKind, message string) RPCError {
	codes := map[ErrorKind]int{
		ErrorKindBundleDigestMismatch: -32012,
		ErrorKindDeadlineExceeded:     -32017,
		ErrorKindInternalError:        -32603,
		ErrorKindInvalidParams:        -32602,
		ErrorKindInvalidRequest:       -32600,
		ErrorKindMethodNotFound:       -32601,
		ErrorKindPreconditionFailed:   -32018,
		ErrorKindProtocolMismatch:     -32011,
		ErrorKindRateLimited:          -32016,
		ErrorKindReplayConflict:       -32014,
		ErrorKindSizeLimitExceeded:    -32015,
		ErrorKindUnauthorizedInstance: -32013,
	}
	retryable := kind == ErrorKindDeadlineExceeded || kind == ErrorKindInternalError || kind == ErrorKindRateLimited
	return RPCError{Code: codes[kind], Kind: kind, Retryable: retryable, Message: message}
}
