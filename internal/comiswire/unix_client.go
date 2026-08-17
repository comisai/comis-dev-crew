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
	"regexp"
	"time"
)

const maximumUnixSocketPathBytes = 103

var instanceCredentialPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{32,256}$`)

type unixRoundTripper struct {
	socketPath string
	bearer     string
	timeout    time.Duration
}

type authenticatedHandshakeRequest struct {
	HandshakeRequest
	Bearer string `json:"bearer"`
}

type authenticatedHealthRequest struct {
	HealthRequest
	Bearer string `json:"bearer"`
}

type authenticatedReportRequest struct {
	ReportRequest
	Bearer string `json:"bearer"`
}

type authenticatedCancelRequest struct {
	CancelRequest
	Bearer string `json:"bearer"`
}

type authenticatedHeartbeatRequest struct {
	HeartbeatRequest
	Bearer string `json:"bearer"`
}

type authenticatedPutEvidenceRequest struct {
	PutEvidenceRequest
	Bearer string `json:"bearer"`
}

type authenticatedReceiveAttentionResponseRequest struct {
	ReceiveAttentionResponseRequest
	Bearer string `json:"bearer"`
}

type authenticatedReleaseRequest struct {
	ReleaseRequest
	Bearer string `json:"bearer"`
}

type responseEnvelope struct {
	Error   json.RawMessage `json:"error"`
	ID      json.RawMessage `json:"id"`
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
}

// NewUnixClient creates the closed capability-service client for an owner-only socket.
func NewUnixClient(socketPath string, timeout time.Duration) (*Client, error) {
	return newUnixClient(socketPath, "", timeout)
}

// NewAuthenticatedUnixClient creates the closed capability-service client and
// adds one protected instance bearer at the transport boundary. The generated
// protocol request DTO remains unchanged.
func NewAuthenticatedUnixClient(socketPath, instanceCredential string, timeout time.Duration) (*Client, error) {
	if !instanceCredentialPattern.MatchString(instanceCredential) {
		return nil, fmt.Errorf("create authenticated Comis wire client: instance credential shape is invalid")
	}
	return newUnixClient(socketPath, instanceCredential, timeout)
}

func newUnixClient(socketPath, bearer string, timeout time.Duration) (*Client, error) {
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
	return newClient(&unixRoundTripper{socketPath: socketPath, bearer: bearer, timeout: timeout}), nil
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
	outbound, err := addInstanceCredential(request, transport.bearer)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(outbound)
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

func addInstanceCredential(request any, bearer string) (any, error) {
	if bearer == "" {
		return request, nil
	}
	switch envelope := request.(type) {
	case HandshakeRequest:
		return authenticatedHandshakeRequest{HandshakeRequest: envelope, Bearer: bearer}, nil
	case HealthRequest:
		return authenticatedHealthRequest{HealthRequest: envelope, Bearer: bearer}, nil
	case ReportRequest:
		return authenticatedReportRequest{ReportRequest: envelope, Bearer: bearer}, nil
	case PutEvidenceRequest:
		return authenticatedPutEvidenceRequest{PutEvidenceRequest: envelope, Bearer: bearer}, nil
	case ReceiveAttentionResponseRequest:
		return authenticatedReceiveAttentionResponseRequest{ReceiveAttentionResponseRequest: envelope, Bearer: bearer}, nil
	case ReleaseRequest:
		return authenticatedReleaseRequest{ReleaseRequest: envelope, Bearer: bearer}, nil
	default:
		return nil, fmt.Errorf("unsupported authenticated Comis wire request %T", request)
	}
}

func outboundOperationID(request any) (OperationID, error) {
	switch envelope := request.(type) {
	case HandshakeRequest:
		return envelope.ID, nil
	case HealthRequest:
		return envelope.ID, nil
	case ReportRequest:
		return envelope.ID, nil
	case PutEvidenceRequest:
		return envelope.ID, nil
	case ReceiveAttentionResponseRequest:
		return envelope.ID, nil
	case ReleaseRequest:
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
	hasResult := len(envelope.Result) > 0
	hasError := len(envelope.Error) > 0
	if hasResult == hasError {
		return fmt.Errorf("comis wire response must contain exactly one of result or error")
	}
	responseID, stringID := decodedID.(string)
	if hasResult && (!stringID || OperationID(responseID) != expectedID) {
		return fmt.Errorf("comis wire response operation ID differs")
	}
	if hasError && decodedID != nil && (!stringID || OperationID(responseID) != expectedID) {
		return fmt.Errorf("comis wire error response operation ID differs")
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
