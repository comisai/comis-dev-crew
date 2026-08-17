package comiswire

import (
	"encoding/json"
	"fmt"
)

// PayloadTarget is the closed fixture and wire-document schema catalog.
type PayloadTarget string

const (
	PayloadRequest               PayloadTarget = "request"
	PayloadAbandonResponse       PayloadTarget = "abandon-response"
	PayloadActivateResponse      PayloadTarget = "activate-response"
	PayloadCancelResponse        PayloadTarget = "cancel-response"
	PayloadErrorResponse         PayloadTarget = "error-response"
	PayloadHandshakeResponse     PayloadTarget = "handshake-response"
	PayloadHealthResponse        PayloadTarget = "health-response"
	PayloadPutEvidenceResponse   PayloadTarget = "put-evidence-response"
	PayloadAttentionResponse     PayloadTarget = "receive-attention-response"
	PayloadReleaseResponse       PayloadTarget = "release-response"
	PayloadReportResponse        PayloadTarget = "report-response"
	PayloadTerminalEventResponse PayloadTarget = "terminal-event-response"
	PayloadMCPCallContext        PayloadTarget = "mcp-call-context"
	PayloadMCPManagedRunResult   PayloadTarget = "mcp-managed-run-result"
)

type requestHeader struct {
	ID      json.RawMessage `json:"id"`
	JSONRPC json.RawMessage `json:"jsonrpc"`
	Method  Method          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// Valid reports whether the target belongs to the pinned closed catalog.
func (target PayloadTarget) Valid() bool {
	switch target {
	case PayloadRequest, PayloadAbandonResponse, PayloadActivateResponse, PayloadCancelResponse, PayloadErrorResponse,
		PayloadHandshakeResponse, PayloadHealthResponse, PayloadPutEvidenceResponse, PayloadAttentionResponse, PayloadReleaseResponse, PayloadReportResponse,
		PayloadTerminalEventResponse, PayloadMCPCallContext, PayloadMCPManagedRunResult:
		return true
	default:
		return false
	}
}

// ValidatePayload applies the exact generated schema and direction-specific wire bound.
func ValidatePayload(target PayloadTarget, contents []byte) error {
	if !target.Valid() {
		return fmt.Errorf("unknown comis payload target %q", target)
	}
	if target != PayloadMCPCallContext && target != PayloadMCPManagedRunResult {
		limit := MaxResponseBytes
		if target == PayloadRequest {
			limit = MaxRequestBytes
		}
		if len(contents) > limit || len(contents)+1 > MaxLineBytes {
			return fmt.Errorf("comis payload exceeds its direction limit")
		}
	}
	schema, _, err := payloadContract(target, contents)
	if err != nil {
		return err
	}
	if err := validateGeneratedJSON(schema, contents); err != nil {
		return fmt.Errorf("validate %s payload: %w", target, err)
	}
	return nil
}

// CanonicalizePayload strictly decodes and re-encodes through its generated DTO.
func CanonicalizePayload(target PayloadTarget, contents []byte) ([]byte, error) {
	if err := ValidatePayload(target, contents); err != nil {
		return nil, err
	}
	_, destination, err := payloadContract(target, contents)
	if err != nil {
		return nil, err
	}
	if err := decodeStrictObject(contents, destination); err != nil {
		return nil, fmt.Errorf("decode %s payload into generated DTO: %w", target, err)
	}
	canonical, err := json.Marshal(destination)
	if err != nil {
		return nil, fmt.Errorf("encode canonical %s payload: %w", target, err)
	}
	return canonical, nil
}

func payloadContract(target PayloadTarget, contents []byte) (string, any, error) {
	switch target {
	case PayloadRequest:
		return requestContract(contents)
	case PayloadAbandonResponse:
		return schemaAbandonResponse, &AbandonResponse{}, nil
	case PayloadActivateResponse:
		return schemaActivateResponse, &ActivateResponse{}, nil
	case PayloadCancelResponse:
		return schemaCancelResponse, &CancelResponse{}, nil
	case PayloadErrorResponse:
		return schemaErrorResponse, &ErrorResponse{}, nil
	case PayloadHandshakeResponse:
		return schemaHandshakeResponse, &HandshakeResponse{}, nil
	case PayloadHealthResponse:
		return schemaHealthResponse, &HealthResponse{}, nil
	case PayloadPutEvidenceResponse:
		return schemaPutEvidenceResponse, &PutEvidenceResponse{}, nil
	case PayloadAttentionResponse:
		return schemaReceiveAttentionResponseResponse, &ReceiveAttentionResponseResponse{}, nil
	case PayloadReleaseResponse:
		return schemaReleaseResponse, &ReleaseResponse{}, nil
	case PayloadReportResponse:
		return schemaReportResponse, &ReportResponse{}, nil
	case PayloadTerminalEventResponse:
		return schemaTerminalEventResponse, &TerminalEventResponse{}, nil
	case PayloadMCPCallContext:
		return schemaMCPCallContext, &MCPCallContext{}, nil
	case PayloadMCPManagedRunResult:
		return schemaMCPManagedRunResult, &MCPManagedRunResult{}, nil
	default:
		return "", nil, fmt.Errorf("unknown comis payload target %q", target)
	}
}

func requestContract(contents []byte) (string, any, error) {
	var header requestHeader
	if err := decodeStrictObject(contents, &header); err != nil {
		return "", nil, fmt.Errorf("decode comis request method: %w", err)
	}
	switch header.Method {
	case MethodCapabilityServicesHandshake:
		return schemaHandshakeRequest, &HandshakeRequest{}, nil
	case MethodCapabilityServicesHealth:
		return schemaHealthRequest, &HealthRequest{}, nil
	case MethodManagedRunsAbandon:
		return schemaAbandonRequest, &AbandonRequest{}, nil
	case MethodManagedRunsActivate:
		return schemaActivateRequest, &ActivateRequest{}, nil
	case MethodManagedRunsCancel:
		return schemaCancelRequest, &CancelRequest{}, nil
	case MethodManagedRunsHeartbeat:
		return schemaHeartbeatRequest, &HeartbeatRequest{}, nil
	case MethodManagedRunsPutEvidence:
		return schemaPutEvidenceRequest, &PutEvidenceRequest{}, nil
	case MethodManagedRunsReceiveAttentionResponse:
		return schemaReceiveAttentionResponseRequest, &ReceiveAttentionResponseRequest{}, nil
	case MethodManagedRunsRelease:
		return schemaReleaseRequest, &ReleaseRequest{}, nil
	case MethodManagedRunsReport:
		return schemaReportRequest, &ReportRequest{}, nil
	case MethodManagedRunsTerminalEvent:
		return schemaTerminalEventRequest, &TerminalEventRequest{}, nil
	default:
		return "", nil, fmt.Errorf("unknown comis request method %q", header.Method)
	}
}
