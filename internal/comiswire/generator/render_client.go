package generator

import (
	"bytes"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func renderClient(manifest bundle.Manifest) (string, error) {
	expected := map[string]bool{
		"capabilityServices.handshake":         false,
		"capabilityServices.health":            false,
		"managedRuns.abandon":                  false,
		"managedRuns.activate":                 false,
		"managedRuns.cancel":                   false,
		"managedRuns.heartbeat":                false,
		"managedRuns.putEvidence":              false,
		"managedRuns.receiveAttentionResponse": false,
		"managedRuns.release":                  false,
		"managedRuns.report":                   false,
		"managedRuns.terminalEvent":            false,
	}
	for _, method := range manifest.MethodCatalog {
		if _, exists := expected[method.Name]; !exists {
			return "", fmt.Errorf("unsupported method %q", method.Name)
		}
		expected[method.Name] = true
	}
	for method, present := range expected {
		if !present {
			return "", fmt.Errorf("required method %q is absent", method)
		}
	}

	var output bytes.Buffer
	output.WriteString(`type roundTripper interface {
	roundTrip(context.Context, any, any) error
}

type Client struct {
	transport roundTripper
}

func sameServiceScopeSet(required, active []ServiceScope) bool {
	requiredSet := make(map[ServiceScope]struct{}, len(required))
	for _, scope := range required {
		if !scope.Valid() {
			return false
		}
		requiredSet[scope] = struct{}{}
	}
	activeSet := make(map[ServiceScope]struct{}, len(active))
	for _, scope := range active {
		if !scope.Valid() {
			return false
		}
		if _, required := requiredSet[scope]; !required {
			return false
		}
		activeSet[scope] = struct{}{}
	}
	return len(activeSet) == len(requiredSet)
}

func newClient(transport roundTripper) *Client {
	return &Client{transport: transport}
}

func (client *Client) Handshake(ctx context.Context, params HandshakeRequestParams) (HandshakeResponseResult, error) {
	if ctx == nil {
		return HandshakeResponseResult{}, fmt.Errorf("handshake context is required")
	}
	if params.ProtocolID != ProtocolID || params.BundleDigest != BundleDigest {
		return HandshakeResponseResult{}, fmt.Errorf("handshake protocol identity or digest differs from the accepted pin")
	}
	request := HandshakeRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodCapabilityServicesHandshake, Params: params}
	if err := validateGeneratedDocument(schemaHandshakeRequest, request); err != nil {
		return HandshakeResponseResult{}, fmt.Errorf("validate handshake request: %w", err)
	}
	var response HandshakeResponse
	if err := client.transport.roundTrip(ctx, request, &response); err != nil {
		return HandshakeResponseResult{}, err
	}
	if err := validateGeneratedDocument(schemaHandshakeResponse, response); err != nil {
		return HandshakeResponseResult{}, fmt.Errorf("validate handshake response: %w", err)
	}
	if response.ID != request.ID || response.Result.ProtocolID != ProtocolID || response.Result.BundleDigest != BundleDigest || response.Result.ServiceInstanceID != params.ServiceInstanceID {
		return HandshakeResponseResult{}, fmt.Errorf("handshake response authority does not match request")
	}
	if !sameServiceScopeSet(params.RequestedScopes, response.Result.ActiveScopes) {
		return HandshakeResponseResult{}, fmt.Errorf("handshake response active scopes differ from requested scopes")
	}
	return response.Result, nil
}

func (client *Client) Health(ctx context.Context, params HealthRequestParams) (HealthResponseResult, error) {
	if ctx == nil {
		return HealthResponseResult{}, fmt.Errorf("health context is required")
	}
	if params.ProtocolID != ProtocolID || params.BundleDigest != BundleDigest {
		return HealthResponseResult{}, fmt.Errorf("health protocol identity or digest differs from the accepted pin")
	}
	request := HealthRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodCapabilityServicesHealth, Params: params}
	if err := validateGeneratedDocument(schemaHealthRequest, request); err != nil {
		return HealthResponseResult{}, fmt.Errorf("validate health request: %w", err)
	}
	var response HealthResponse
	if err := client.transport.roundTrip(ctx, request, &response); err != nil {
		return HealthResponseResult{}, err
	}
	if err := validateGeneratedDocument(schemaHealthResponse, response); err != nil {
		return HealthResponseResult{}, fmt.Errorf("validate health response: %w", err)
	}
	if response.ID != request.ID || response.Result.ProtocolID != ProtocolID || response.Result.BundleDigest != BundleDigest || response.Result.ServiceInstanceID != params.ServiceInstanceID {
		return HealthResponseResult{}, fmt.Errorf("health response authority does not match request")
	}
	return response.Result, nil
}

func (client *Client) Report(ctx context.Context, params ReportRequestParams) (ReportResponseResult, error) {
	if ctx == nil {
		return ReportResponseResult{}, fmt.Errorf("report context is required")
	}
	detailsBytes := 0
	if params.Details != nil {
		detailsBytes = len([]byte(*params.Details))
	}
	if len([]byte(params.Summary))+detailsBytes > MaxReportBytes {
		return ReportResponseResult{}, fmt.Errorf("report summary and details exceed %d UTF-8 bytes", MaxReportBytes)
	}
	request := ReportRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsReport, Params: params}
	if err := validateGeneratedDocument(schemaReportRequest, request); err != nil {
		return ReportResponseResult{}, fmt.Errorf("validate report request: %w", err)
	}
	var response ReportResponse
	if err := client.transport.roundTrip(ctx, request, &response); err != nil {
		return ReportResponseResult{}, err
	}
	if err := validateGeneratedDocument(schemaReportResponse, response); err != nil {
		return ReportResponseResult{}, fmt.Errorf("validate report response: %w", err)
	}
	if response.ID != request.ID || response.Result.ManagedRunID != params.ManagedRunID || response.Result.ServiceReportID != params.ServiceReportID {
		return ReportResponseResult{}, fmt.Errorf("report response identity does not match request")
	}
	return response.Result, nil
}

func (client *Client) PutEvidence(ctx context.Context, params PutEvidenceRequestParams) (PutEvidenceResponseResult, error) {
	if ctx == nil {
		return PutEvidenceResponseResult{}, fmt.Errorf("put evidence context is required")
	}
	body, err := base64.StdEncoding.DecodeString(params.BodyBase64)
	if err != nil || len(body) == 0 || len(body) > MaxEvidenceBytes {
		return PutEvidenceResponseResult{}, fmt.Errorf("evidence body is invalid")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(body))
	if digest != params.ContentHash {
		return PutEvidenceResponseResult{}, fmt.Errorf("evidence body differs from its content hash")
	}
	if params.VerificationLevel == EvidenceVerificationLevelHostVerified {
		return PutEvidenceResponseResult{}, fmt.Errorf("host verification is reserved")
	}
	request := PutEvidenceRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsPutEvidence, Params: params}
	if err := validateGeneratedDocument(schemaPutEvidenceRequest, request); err != nil {
		return PutEvidenceResponseResult{}, fmt.Errorf("validate put evidence request: %w", err)
	}
	var response PutEvidenceResponse
	if err := client.transport.roundTrip(ctx, request, &response); err != nil {
		return PutEvidenceResponseResult{}, err
	}
	if err := validateGeneratedDocument(schemaPutEvidenceResponse, response); err != nil {
		return PutEvidenceResponseResult{}, fmt.Errorf("validate put evidence response: %w", err)
	}
	if response.ID != request.ID || response.Result.ManagedRunID != params.ManagedRunID ||
		response.Result.EvidenceRef != params.EvidenceRef || response.Result.ContentHash != params.ContentHash ||
		response.Result.VerificationLevel != params.VerificationLevel {
		return PutEvidenceResponseResult{}, fmt.Errorf("put evidence response identity does not match request")
	}
	return response.Result, nil
}

func (client *Client) ReceiveAttentionResponse(ctx context.Context, params ReceiveAttentionResponseRequestParams) (ReceiveAttentionResponseResponseResult, error) {
	if ctx == nil {
		return ReceiveAttentionResponseResponseResult{}, fmt.Errorf("receive attention response context is required")
	}
	request := ReceiveAttentionResponseRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsReceiveAttentionResponse, Params: params}
	if err := validateGeneratedDocument(schemaReceiveAttentionResponseRequest, request); err != nil {
		return ReceiveAttentionResponseResponseResult{}, fmt.Errorf("validate receive attention response request: %w", err)
	}
	var response ReceiveAttentionResponseResponse
	if err := client.transport.roundTrip(ctx, request, &response); err != nil {
		return ReceiveAttentionResponseResponseResult{}, err
	}
	if err := validateGeneratedDocument(schemaReceiveAttentionResponseResponse, response); err != nil {
		return ReceiveAttentionResponseResponseResult{}, fmt.Errorf("validate receive attention response response: %w", err)
	}
	if response.ID != request.ID || response.Result.ManagedRunID != params.ManagedRunID || response.Result.ExternalKey != params.ExternalKey {
		return ReceiveAttentionResponseResponseResult{}, fmt.Errorf("receive attention response identity does not match request")
	}
	return response.Result, nil
}

func (client *Client) Release(ctx context.Context, params ReleaseRequestParams) (ReleaseResponseResult, error) {
	if ctx == nil {
		return ReleaseResponseResult{}, fmt.Errorf("release context is required")
	}
	request := ReleaseRequest{JSONRPC: JSONRPCVersion, ID: params.OperationID, Method: MethodManagedRunsRelease, Params: params}
	if err := validateGeneratedDocument(schemaReleaseRequest, request); err != nil {
		return ReleaseResponseResult{}, fmt.Errorf("validate release request: %w", err)
	}
	var response ReleaseResponse
	if err := client.transport.roundTrip(ctx, request, &response); err != nil {
		return ReleaseResponseResult{}, err
	}
	if err := validateGeneratedDocument(schemaReleaseResponse, response); err != nil {
		return ReleaseResponseResult{}, fmt.Errorf("validate release response: %w", err)
	}
	if response.ID != request.ID || response.Result.ManagedRunID != params.ManagedRunID ||
		response.Result.WorkspaceLeaseID != params.WorkspaceLeaseID || response.Result.Disposition != params.Disposition ||
		response.Result.ReleasedAtMs != params.ReleasedAtMs || response.Result.State != ManagedRunState("released") {
		return ReleaseResponseResult{}, fmt.Errorf("release response identity does not match request")
	}
	return response.Result, nil
}
`)
	return output.String(), nil
}
