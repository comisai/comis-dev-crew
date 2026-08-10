package comiswire

import (
	"context"
	"errors"
	"testing"
)

type recordingTransport struct {
	request  any
	response func(any)
	err      error
}

func (transport *recordingTransport) roundTrip(_ context.Context, request, response any) error {
	transport.request = request
	if transport.response != nil {
		transport.response(response)
	}
	return transport.err
}

func TestGeneratedClientBuildsClosedOperationEnvelopes(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*Client) error
		assert func(*testing.T, any)
	}{
		{
			name: "handshake",
			invoke: func(client *Client) error {
				_, err := client.Handshake(context.Background(), HandshakeRequestParams{
					ProtocolID:        ProtocolID,
					BundleDigest:      BundleDigest,
					OperationID:       "operation_handshake",
					ServiceInstanceID: "service-instance_a",
					RequestedScopes:   []ServiceScope{ServiceScopeHealth, ServiceScopeReport},
				})
				return err
			},
			assert: func(t *testing.T, request any) {
				t.Helper()
				envelope, ok := request.(HandshakeRequest)
				if !ok || envelope.JSONRPC != JSONRPCVersion || envelope.ID != envelope.Params.OperationID || envelope.Method != MethodCapabilityServicesHandshake {
					t.Fatalf("unexpected handshake envelope: %#v", request)
				}
			},
		},
		{
			name: "health",
			invoke: func(client *Client) error {
				_, err := client.Health(context.Background(), HealthRequestParams{
					ProtocolID: ProtocolID, BundleDigest: BundleDigest, OperationID: "operation_health", ServiceInstanceID: "service-instance_a",
				})
				return err
			},
			assert: func(t *testing.T, request any) {
				t.Helper()
				envelope, ok := request.(HealthRequest)
				if !ok || envelope.ID != envelope.Params.OperationID || envelope.Method != MethodCapabilityServicesHealth {
					t.Fatalf("unexpected health envelope: %#v", request)
				}
			},
		},
		{
			name: "report",
			invoke: func(client *Client) error {
				_, err := client.Report(context.Background(), ReportRequestParams{
					OperationID: "operation_report", ManagedRunID: "managed-run_a", ServiceReportID: "service-report_a", Kind: ReportKindProgress, Summary: "progress",
				})
				return err
			},
			assert: func(t *testing.T, request any) {
				t.Helper()
				envelope, ok := request.(ReportRequest)
				if !ok || envelope.ID != envelope.Params.OperationID || envelope.Method != MethodManagedRunsReport {
					t.Fatalf("unexpected report envelope: %#v", request)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingTransport{err: errors.New("stop after request capture")}
			if err := test.invoke(newClient(transport)); err == nil {
				t.Fatal("expected transport error")
			}
			test.assert(t, transport.request)
		})
	}
}

func TestGeneratedClientRejectsResponseIdentityAndAuthorityDrift(t *testing.T) {
	tests := []struct {
		name     string
		response HandshakeResponse
	}{
		{name: "response envelope ID differs", response: validHandshakeResponse(func(response *HandshakeResponse) { response.ID = "operation_other" })},
		{name: "protocol identifier differs", response: validHandshakeResponse(func(response *HandshakeResponse) { response.Result.ProtocolID = "comis.capability-service/2" })},
		{name: "bundle digest differs", response: validHandshakeResponse(func(response *HandshakeResponse) {
			response.Result.BundleDigest = "0000000000000000000000000000000000000000000000000000000000000000"
		})},
		{name: "requested scope is inactive", response: validHandshakeResponse(func(response *HandshakeResponse) { response.Result.ActiveScopes = []ServiceScope{ServiceScopeHealth} })},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingTransport{response: func(target any) {
				*(target.(*HandshakeResponse)) = test.response
			}}
			_, err := newClient(transport).Handshake(context.Background(), HandshakeRequestParams{
				ProtocolID: ProtocolID, BundleDigest: BundleDigest, OperationID: "operation_handshake", ServiceInstanceID: "service-instance_a",
				RequestedScopes: []ServiceScope{ServiceScopeHealth, ServiceScopeReport},
			})
			if err == nil {
				t.Fatal("expected response authority rejection")
			}
		})
	}
}

func TestGeneratedClientComparesNegotiatedScopesAsSets(t *testing.T) {
	params := HandshakeRequestParams{
		ProtocolID: ProtocolID, BundleDigest: BundleDigest, OperationID: "operation_handshake",
		ServiceInstanceID: "service-instance_a",
		RequestedScopes:   []ServiceScope{ServiceScopeHealth, ServiceScopeReport},
	}
	accepted := &recordingTransport{response: func(target any) {
		*(target.(*HandshakeResponse)) = validHandshakeResponse(func(response *HandshakeResponse) {
			response.Result.ActiveScopes = []ServiceScope{ServiceScopeReport, ServiceScopeHealth, ServiceScopeReport}
		})
	}}
	if _, err := newClient(accepted).Handshake(context.Background(), params); err != nil {
		t.Fatalf("Handshake(reordered duplicate scopes) error = %v", err)
	}
	extra := &recordingTransport{response: func(target any) {
		*(target.(*HandshakeResponse)) = validHandshakeResponse(func(response *HandshakeResponse) {
			response.Result.ActiveScopes = []ServiceScope{ServiceScopeReport, ServiceScopeHealth, ServiceScopeWorkspaceLease}
		})
	}}
	if _, err := newClient(extra).Handshake(context.Background(), params); err == nil {
		t.Fatal("Handshake(unexpected active scope) error = nil")
	}
}

func TestGeneratedClientRejectsSupersededBundleDigest(t *testing.T) {
	const supersededDigest = "e87e69511ea9e01ea2383cd211f9946233fdbe1ce8edf016e76ce55eae683297"
	if BundleDigest == supersededDigest {
		t.Fatal("generated client still accepts the superseded bundle digest")
	}
	transport := &recordingTransport{}
	_, err := newClient(transport).Handshake(context.Background(), HandshakeRequestParams{
		ProtocolID: ProtocolID, BundleDigest: supersededDigest,
		OperationID: "operation_superseded_digest", ServiceInstanceID: "service-instance_a",
		RequestedScopes: []ServiceScope{ServiceScopeHealth, ServiceScopeReport},
	})
	if err == nil || transport.request != nil {
		t.Fatalf("Handshake(superseded digest) error = %v, request = %#v", err, transport.request)
	}
}

func validHandshakeResponse(mutate func(*HandshakeResponse)) HandshakeResponse {
	response := HandshakeResponse{
		JSONRPC: JSONRPCVersion,
		ID:      "operation_handshake",
		Result: HandshakeResponseResult{
			ProtocolID: ProtocolID, BundleDigest: BundleDigest, ServiceInstanceID: "service-instance_a",
			ActiveScopes: []ServiceScope{ServiceScopeHealth, ServiceScopeReport},
			Limits: ProtocolLimits{
				MaxEvidenceBytes: MaxEvidenceBytes, MaxInFlightRequests: MaxInFlightRequests, MaxLineBytes: MaxLineBytes,
				MaxReportBytes: MaxReportBytes, MaxRequestBytes: MaxRequestBytes, MaxResponseBytes: MaxResponseBytes,
				ReportRetentionDays: ReportRetentionDays,
			},
		},
	}
	mutate(&response)
	return response
}
