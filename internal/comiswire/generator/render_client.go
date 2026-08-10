package generator

import (
	"bytes"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func renderClient(manifest bundle.Manifest) (string, error) {
	expected := map[string]bool{
		"capabilityServices.handshake": false,
		"capabilityServices.health":    false,
		"managedRuns.abandon":          false,
		"managedRuns.activate":         false,
		"managedRuns.report":           false,
		"managedRuns.terminalEvent":    false,
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
	for _, requested := range params.RequestedScopes {
		active := false
		for _, candidate := range response.Result.ActiveScopes {
			if requested == candidate {
				active = true
				break
			}
		}
		if !active {
			return HandshakeResponseResult{}, fmt.Errorf("handshake response omits requested scope %q", requested)
		}
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
`)
	return output.String(), nil
}
