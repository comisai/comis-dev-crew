package comiswire

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const maximumUnixSocketPathBytes = 103

type unixRoundTripper struct {
	socketPath string
	timeout    time.Duration
}

type responseEnvelope struct {
	Error   json.RawMessage `json:"error"`
	ID      json.RawMessage `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
}

// NewUnixClient creates the closed capability-service client for an owner-only socket.
func NewUnixClient(socketPath string, timeout time.Duration) (*Client, error) {
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath {
		return nil, fmt.Errorf("create Comis wire client: socket path must be absolute and canonical")
	}
	if len([]byte(socketPath)) > maximumUnixSocketPathBytes {
		return nil, fmt.Errorf("create Comis wire client: socket path is too long")
	}
	canonicalParent, err := filepath.EvalSymlinks(filepath.Dir(socketPath))
	if err != nil {
		return nil, fmt.Errorf("create Comis wire client: canonicalize socket parent: %w", err)
	}
	if canonicalParent != filepath.Dir(socketPath) {
		return nil, fmt.Errorf("create Comis wire client: socket parent contains a symlink")
	}
	if timeout <= 0 {
		return nil, fmt.Errorf("create Comis wire client: timeout must be positive")
	}
	return newClient(&unixRoundTripper{socketPath: socketPath, timeout: timeout}), nil
}

func (transport *unixRoundTripper) roundTrip(ctx context.Context, request, response any) (resultErr error) {
	if ctx == nil {
		return fmt.Errorf("comis wire round trip: context is required")
	}
	expectedID, err := outboundOperationID(request)
	if err != nil {
		return err
	}
	before, err := inspectOwnerOnlySocket(transport.socketPath)
	if err != nil {
		return err
	}
	callContext, cancel := context.WithTimeout(ctx, transport.timeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(callContext, "unix", transport.socketPath)
	if err != nil {
		return fmt.Errorf("connect to Comis capability socket: %w", err)
	}
	defer func() {
		if closeErr := connection.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close Comis capability socket: %w", closeErr))
		}
	}()
	after, err := inspectOwnerOnlySocket(transport.socketPath)
	if err != nil {
		return err
	}
	if !os.SameFile(before, after) {
		return fmt.Errorf("comis capability socket identity changed during connection")
	}
	if deadline, ok := callContext.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return fmt.Errorf("set Comis capability socket deadline: %w", err)
		}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode Comis wire request: %w", err)
	}
	if len(encoded) > MaxRequestBytes {
		return fmt.Errorf("comis wire request exceeds %d bytes", MaxRequestBytes)
	}
	encoded = append(encoded, '\n')
	if _, err := io.Copy(connection, bytes.NewReader(encoded)); err != nil {
		return contextOrTransportError(callContext, "write Comis wire request", err)
	}
	reader := bufio.NewReaderSize(connection, MaxResponseBytes+1)
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > MaxResponseBytes+1 {
		return fmt.Errorf("comis wire response exceeds %d bytes", MaxResponseBytes)
	}
	if err != nil {
		return contextOrTransportError(callContext, "read Comis wire response", err)
	}
	return decodeWireResponse(line[:len(line)-1], expectedID, response)
}

func outboundOperationID(request any) (OperationID, error) {
	switch envelope := request.(type) {
	case HandshakeRequest:
		return envelope.ID, nil
	case HealthRequest:
		return envelope.ID, nil
	case ReportRequest:
		return envelope.ID, nil
	default:
		return "", fmt.Errorf("unsupported outbound Comis wire request %T", request)
	}
}

func inspectOwnerOnlySocket(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect Comis capability socket: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return nil, fmt.Errorf("comis capability endpoint is not a non-symlink Unix socket")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("comis capability socket is not owner-only")
	}
	return info, nil
}

func decodeWireResponse(contents []byte, expectedID OperationID, response any) error {
	var envelope responseEnvelope
	if err := decodeStrictObject(contents, &envelope); err != nil {
		return fmt.Errorf("decode Comis wire response envelope: %w", err)
	}
	if envelope.JSONRPC != JSONRPCVersion {
		return fmt.Errorf("comis wire response JSON-RPC version differs")
	}
	decodedID, err := decodeStrictValue(envelope.ID)
	if err != nil {
		return fmt.Errorf("decode Comis wire response ID: %w", err)
	}
	responseID, ok := decodedID.(string)
	if !ok || OperationID(responseID) != expectedID {
		return fmt.Errorf("comis wire response operation ID differs")
	}
	hasResult := len(envelope.Result) > 0
	hasError := len(envelope.Error) > 0
	if hasResult == hasError {
		return fmt.Errorf("comis wire response must contain exactly one of result or error")
	}
	if hasError {
		if err := validateGeneratedJSON(schemaErrorResponse, contents); err != nil {
			return fmt.Errorf("validate Comis wire error response: %w", err)
		}
		var failure ErrorResponse
		if err := decodeStrictObject(contents, &failure); err != nil {
			return fmt.Errorf("decode Comis wire error response: %w", err)
		}
		return failure.Error
	}
	if err := decodeStrictObject(contents, response); err != nil {
		return fmt.Errorf("decode Comis wire result response: %w", err)
	}
	return nil
}

func decodeStrictObject(contents []byte, destination any) error {
	if err := rejectDuplicateJSONNames(contents); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return expectJSONEnd(decoder)
}

func contextOrTransportError(ctx context.Context, operation string, cause error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, cause)
}
