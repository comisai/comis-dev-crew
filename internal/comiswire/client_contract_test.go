package comiswire

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
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
			name: "attention response",
			invoke: func(client *Client) error {
				_, err := client.ReceiveAttentionResponse(context.Background(), ReceiveAttentionResponseRequestParams{
					OperationID: "operation_attention", ManagedRunID: "managed-run_a", ExternalKey: "decision_a",
				})
				return err
			},
			assert: func(t *testing.T, request any) {
				t.Helper()
				envelope, ok := request.(ReceiveAttentionResponseRequest)
				if !ok || envelope.ID != envelope.Params.OperationID || envelope.Method != MethodManagedRunsReceiveAttentionResponse {
					t.Fatalf("unexpected attention response envelope: %#v", request)
				}
			},
		},
		{
			name: "release",
			invoke: func(client *Client) error {
				_, err := client.Release(context.Background(), ReleaseRequestParams{
					OperationID: "operation_release", ManagedRunID: "managed-run_a",
					WorkspaceLeaseID: "workspace-lease_a", Disposition: "reap_safe", ReleasedAtMs: 1_800_000_000_000,
				})
				return err
			},
			assert: func(t *testing.T, request any) {
				t.Helper()
				envelope, ok := request.(ReleaseRequest)
				if !ok || envelope.ID != envelope.Params.OperationID || envelope.Method != MethodManagedRunsRelease {
					t.Fatalf("unexpected release envelope: %#v", request)
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

func TestGeneratedClientValidatesAttentionResponseStateAndAuthority(t *testing.T) {
	params := ReceiveAttentionResponseRequestParams{
		OperationID: "operation_attention", ManagedRunID: "managed-run_a", ExternalKey: "decision_a",
	}
	if _, err := newClient(&recordingTransport{}).ReceiveAttentionResponse(missingContext(), params); err == nil {
		t.Fatal("ReceiveAttentionResponse(nil context) error = nil")
	}
	invalid := params
	invalid.OperationID = "bad operation"
	if _, err := newClient(&recordingTransport{}).ReceiveAttentionResponse(context.Background(), invalid); err == nil {
		t.Fatal("ReceiveAttentionResponse(invalid request) error = nil")
	}
	pending := &recordingTransport{response: func(target any) {
		*(target.(*ReceiveAttentionResponseResponse)) = ReceiveAttentionResponseResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: ReceiveAttentionResponseResponseResult{
				ManagedRunID: params.ManagedRunID, ExternalKey: params.ExternalKey, State: ManagedRunStatePending,
			},
		}
	}}
	result, err := newClient(pending).ReceiveAttentionResponse(context.Background(), params)
	if err != nil || result.State != ManagedRunStatePending || result.Response != nil {
		t.Fatalf("ReceiveAttentionResponse(pending) = %#v, %v", result, err)
	}
	answer := "Use monotonic issue-N values."
	delivered := &recordingTransport{response: func(target any) {
		*(target.(*ReceiveAttentionResponseResponse)) = ReceiveAttentionResponseResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: ReceiveAttentionResponseResponseResult{
				ManagedRunID: params.ManagedRunID, ExternalKey: params.ExternalKey,
				State: ManagedRunStateDelivered, Response: &answer,
			},
		}
	}}
	result, err = newClient(delivered).ReceiveAttentionResponse(context.Background(), params)
	if err != nil || result.Response == nil || *result.Response != answer {
		t.Fatalf("ReceiveAttentionResponse(delivered) = %#v, %v", result, err)
	}
	drifted := &recordingTransport{response: func(target any) {
		*(target.(*ReceiveAttentionResponseResponse)) = ReceiveAttentionResponseResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: ReceiveAttentionResponseResponseResult{
				ManagedRunID: "managed-run_other", ExternalKey: params.ExternalKey, State: ManagedRunStatePending,
			},
		}
	}}
	if _, err := newClient(drifted).ReceiveAttentionResponse(context.Background(), params); err == nil {
		t.Fatal("ReceiveAttentionResponse(authority drift) error = nil")
	}
	invalidState := &recordingTransport{response: func(target any) {
		*(target.(*ReceiveAttentionResponseResponse)) = ReceiveAttentionResponseResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: ReceiveAttentionResponseResponseResult{
				ManagedRunID: params.ManagedRunID, ExternalKey: params.ExternalKey,
				State: ManagedRunStateDelivered,
			},
		}
	}}
	if _, err := newClient(invalidState).ReceiveAttentionResponse(context.Background(), params); err == nil {
		t.Fatal("ReceiveAttentionResponse(delivered without body) error = nil")
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

func TestGeneratedClientPutEvidenceValidatesBodyAuthorityAndResponseIdentity(t *testing.T) {
	body := []byte("bounded candidate evidence")
	contentHash := fmt.Sprintf("%x", sha256.Sum256(body))
	params := PutEvidenceRequestParams{
		OperationID: "operation_evidence", ManagedRunID: "managed-run_a", EvidenceRef: "evidence_a",
		Kind: "candidate_bundle", SubjectDigest: contentHash, ObservedAtMs: 1_800_000_000_000,
		ContentHash: contentHash, VerificationLevel: EvidenceVerificationLevelAdapterVerified,
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	}
	if _, err := newClient(&recordingTransport{}).PutEvidence(missingContext(), params); err == nil {
		t.Fatal("PutEvidence(nil context) error = nil")
	}
	invalidBody := params
	invalidBody.BodyBase64 = "not-base64"
	if _, err := newClient(&recordingTransport{}).PutEvidence(context.Background(), invalidBody); err == nil {
		t.Fatal("PutEvidence(invalid body) error = nil")
	}
	wrongHash := params
	wrongHash.ContentHash = fmt.Sprintf("%064x", 1)
	if _, err := newClient(&recordingTransport{}).PutEvidence(context.Background(), wrongHash); err == nil {
		t.Fatal("PutEvidence(wrong hash) error = nil")
	}
	hostVerified := params
	hostVerified.VerificationLevel = EvidenceVerificationLevelHostVerified
	if _, err := newClient(&recordingTransport{}).PutEvidence(context.Background(), hostVerified); err == nil {
		t.Fatal("PutEvidence(host verified) error = nil")
	}
	invalidRequest := params
	invalidRequest.OperationID = "bad operation"
	if _, err := newClient(&recordingTransport{}).PutEvidence(context.Background(), invalidRequest); err == nil {
		t.Fatal("PutEvidence(invalid request) error = nil")
	}
	transportFailure := &recordingTransport{err: errors.New("transport unavailable")}
	if _, err := newClient(transportFailure).PutEvidence(context.Background(), params); err == nil {
		t.Fatal("PutEvidence(transport failure) error = nil")
	}
	invalidResponse := &recordingTransport{}
	if _, err := newClient(invalidResponse).PutEvidence(context.Background(), params); err == nil {
		t.Fatal("PutEvidence(invalid response) error = nil")
	}
	drifted := &recordingTransport{response: func(target any) {
		*(target.(*PutEvidenceResponse)) = PutEvidenceResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: PutEvidenceResponseResult{
				ManagedRunID: "managed-run_other", EvidenceRef: params.EvidenceRef,
				ContentHash: params.ContentHash, VerificationLevel: params.VerificationLevel,
			},
		}
	}}
	if _, err := newClient(drifted).PutEvidence(context.Background(), params); err == nil {
		t.Fatal("PutEvidence(response drift) error = nil")
	}
	accepted := &recordingTransport{response: func(target any) {
		*(target.(*PutEvidenceResponse)) = PutEvidenceResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: PutEvidenceResponseResult{
				ManagedRunID: params.ManagedRunID, EvidenceRef: params.EvidenceRef,
				ContentHash: params.ContentHash, VerificationLevel: params.VerificationLevel,
			},
		}
	}}
	result, err := newClient(accepted).PutEvidence(context.Background(), params)
	if err != nil || result.ManagedRunID != params.ManagedRunID || result.ContentHash != params.ContentHash {
		t.Fatalf("PutEvidence() = %#v, %v", result, err)
	}
}

func TestGeneratedClientReleaseValidatesRequestAndResponseAuthority(t *testing.T) {
	params := ReleaseRequestParams{
		OperationID: "operation_release", ManagedRunID: "managed-run_a",
		WorkspaceLeaseID: "workspace-lease_a", Disposition: "reap_safe", ReleasedAtMs: 1_800_000_000_000,
	}
	if _, err := newClient(&recordingTransport{}).Release(missingContext(), params); err == nil {
		t.Fatal("Release(nil context) error = nil")
	}
	invalid := params
	invalid.OperationID = "bad operation"
	if _, err := newClient(&recordingTransport{}).Release(context.Background(), invalid); err == nil {
		t.Fatal("Release(invalid request) error = nil")
	}
	if _, err := newClient(&recordingTransport{err: errors.New("transport unavailable")}).Release(context.Background(), params); err == nil {
		t.Fatal("Release(transport failure) error = nil")
	}
	if _, err := newClient(&recordingTransport{}).Release(context.Background(), params); err == nil {
		t.Fatal("Release(invalid response) error = nil")
	}
	drifted := &recordingTransport{response: func(target any) {
		*(target.(*ReleaseResponse)) = ReleaseResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: ReleaseResponseResult{
				ManagedRunID: "managed-run_other", WorkspaceLeaseID: params.WorkspaceLeaseID,
				Disposition: params.Disposition, ReleasedAtMs: params.ReleasedAtMs, State: ManagedRunState("released"),
			},
		}
	}}
	if _, err := newClient(drifted).Release(context.Background(), params); err == nil {
		t.Fatal("Release(response drift) error = nil")
	}
	accepted := &recordingTransport{response: func(target any) {
		*(target.(*ReleaseResponse)) = ReleaseResponse{
			JSONRPC: JSONRPCVersion, ID: params.OperationID,
			Result: ReleaseResponseResult{
				ManagedRunID: params.ManagedRunID, WorkspaceLeaseID: params.WorkspaceLeaseID,
				Disposition: params.Disposition, ReleasedAtMs: params.ReleasedAtMs, State: ManagedRunState("released"),
			},
		}
	}}
	result, err := newClient(accepted).Release(context.Background(), params)
	if err != nil || result.ManagedRunID != params.ManagedRunID || result.ReleasedAtMs != params.ReleasedAtMs {
		t.Fatalf("Release() = %#v, %v", result, err)
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
