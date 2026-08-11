package comiswire

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestWireSchemaValidatorCoversClosedDraftSubset(t *testing.T) {
	validObject := `{
		"additionalProperties":false,
		"properties":{
			"active":{"type":"boolean","const":true},
			"count":{"type":"integer","minimum":1,"exclusiveMinimum":0,"maximum":3},
			"kind":{"type":"string","enum":["one","two"]},
			"nullable":{"anyOf":[{"type":"string"},{"type":"null"}]},
			"tags":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"string","minLength":2,"maxLength":4,"pattern":"^[a-z]+$"}}
		},
		"required":["active","count","kind","nullable","tags"],
		"type":"object"
	}`
	tests := []struct {
		name    string
		schema  string
		payload string
		valid   bool
	}{
		{name: "closed object accepted", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":null,"tags":["ab"]}`, valid: true},
		{name: "required property missing", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":null}`, valid: false},
		{name: "unknown property rejected", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":null,"tags":["ab"],"extra":1}`, valid: false},
		{name: "boolean type rejected", schema: validObject, payload: `{"active":"true","count":2,"kind":"one","nullable":null,"tags":["ab"]}`, valid: false},
		{name: "boolean without constant rejects string", schema: `{"type":"boolean"}`, payload: `"true"`, valid: false},
		{name: "constant rejected", schema: validObject, payload: `{"active":false,"count":2,"kind":"one","nullable":null,"tags":["ab"]}`, valid: false},
		{name: "enum rejected", schema: validObject, payload: `{"active":true,"count":2,"kind":"three","nullable":null,"tags":["ab"]}`, valid: false},
		{name: "integer fraction rejected", schema: validObject, payload: `{"active":true,"count":1.5,"kind":"one","nullable":null,"tags":["ab"]}`, valid: false},
		{name: "minimum rejected", schema: validObject, payload: `{"active":true,"count":-1,"kind":"one","nullable":null,"tags":["ab"]}`, valid: false},
		{name: "exclusive minimum rejected", schema: `{"type":"number","exclusiveMinimum":0}`, payload: `0`, valid: false},
		{name: "maximum rejected", schema: validObject, payload: `{"active":true,"count":4,"kind":"one","nullable":null,"tags":["ab"]}`, valid: false},
		{name: "number type rejected", schema: `{"type":"number"}`, payload: `"1"`, valid: false},
		{name: "array type rejected", schema: `{"type":"array"}`, payload: `{}`, valid: false},
		{name: "object type rejected", schema: `{"type":"object"}`, payload: `[]`, valid: false},
		{name: "open object accepts extra properties", schema: `{"type":"object"}`, payload: `{"anything":1}`, valid: true},
		{name: "array minimum rejected", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":null,"tags":[]}`, valid: false},
		{name: "array maximum rejected", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":null,"tags":["ab","cd","ef"]}`, valid: false},
		{name: "array item rejected", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":null,"tags":["A"]}`, valid: false},
		{name: "string type rejected", schema: `{"type":"string"}`, payload: `1`, valid: false},
		{name: "string maximum rejected", schema: `{"type":"string","maxLength":1}`, payload: `"ab"`, valid: false},
		{name: "string pattern rejected", schema: `{"type":"string","pattern":"^[a-z]+$"}`, payload: `"A"`, valid: false},
		{name: "invalid schema pattern rejected", schema: `{"type":"string","pattern":"["}`, payload: `"a"`, valid: false},
		{name: "nullable string accepted", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":"ok","tags":["ab"]}`, valid: true},
		{name: "nullable union rejected", schema: validObject, payload: `{"active":true,"count":2,"kind":"one","nullable":2,"tags":["ab"]}`, valid: false},
		{name: "one of accepts one", schema: `{"oneOf":[{"type":"string"},{"type":"number"}]}`, payload: `1`, valid: true},
		{name: "one of rejects overlap", schema: `{"oneOf":[{"type":"number"},{"type":"number"}]}`, payload: `1`, valid: false},
		{name: "null type rejected", schema: `{"type":"null"}`, payload: `false`, valid: false},
		{name: "unsupported schema type rejected", schema: `{"type":"function"}`, payload: `null`, valid: false},
		{name: "empty schema accepts value", schema: `{}`, payload: `null`, valid: true},
		{name: "duplicate JSON name rejected", schema: `{"type":"object"}`, payload: `{"a":1,"a":2}`, valid: false},
		{name: "trailing JSON rejected", schema: `{"type":"object"}`, payload: `{} {}`, valid: false},
		{name: "malformed JSON rejected", schema: `{"type":"object"}`, payload: `{`, valid: false},
		{name: "malformed generated schema rejected", schema: `{`, payload: `{}`, valid: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateGeneratedJSON(test.schema, []byte(test.payload))
			if test.valid && err != nil {
				t.Fatalf("expected acceptance: %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}

func TestGeneratedDocumentRejectsEncodingAndLineLimitFailures(t *testing.T) {
	if err := validateGeneratedDocument(`{"type":"string"}`, make(chan int)); err == nil {
		t.Fatal("expected unsupported value encoding rejection")
	}
	if err := validateGeneratedDocument(`{"type":"string"}`, strings.Repeat("x", MaxLineBytes)); err == nil {
		t.Fatal("expected generated line limit rejection")
	}
}

func TestWireSchemaValidatorRejectsMalformedEmbeddedConstantsAndBounds(t *testing.T) {
	invalid := json.RawMessage("{")
	if err := validateWireValue(wireSchema{Const: invalid}, "value", "$"); err == nil {
		t.Fatal("expected malformed constant rejection")
	}
	if err := validateWireValue(wireSchema{Enum: []json.RawMessage{invalid}}, "value", "$"); err == nil {
		t.Fatal("expected malformed enum rejection")
	}
	badNumber := json.Number("not-a-number")
	if err := validateWireNumber(wireSchema{}, badNumber, "$", false); err == nil {
		t.Fatal("expected malformed number rejection")
	}
	value := json.Number("1")
	if err := validateWireNumber(wireSchema{Minimum: &badNumber}, value, "$", false); err == nil {
		t.Fatal("expected malformed numeric bound rejection")
	}
	if _, err := decodeStrictValue([]byte{'"', 0xff, '"'}); err == nil {
		t.Fatal("expected invalid UTF-8 rejection")
	}
}

func TestGeneratedClosedClientAcceptsValidHealthAndReportResponses(t *testing.T) {
	healthTransport := &recordingTransport{response: func(target any) {
		*(target.(*HealthResponse)) = HealthResponse{
			JSONRPC: JSONRPCVersion,
			ID:      "operation_health",
			Result: HealthResponseResult{
				BundleDigest: BundleDigest, ObservedAtMs: 1, ProtocolID: ProtocolID, ReasonCodes: []string{},
				ServiceInstanceID: "service-instance_a", Status: HealthStatusHealthy,
			},
		}
	}}
	if _, err := newClient(healthTransport).Health(context.Background(), HealthRequestParams{
		BundleDigest: BundleDigest, OperationID: "operation_health", ProtocolID: ProtocolID, ServiceInstanceID: "service-instance_a",
	}); err != nil {
		t.Fatalf("valid health response: %v", err)
	}

	reportTransport := &recordingTransport{response: func(target any) {
		*(target.(*ReportResponse)) = ReportResponse{
			JSONRPC: JSONRPCVersion,
			ID:      "operation_report",
			Result:  ReportResponseResult{AcceptedSequence: 1, ManagedRunID: "managed-run_a", RetainedUntilMs: 1, ServiceReportID: "service-report_a"},
		}
	}}
	if _, err := newClient(reportTransport).Report(context.Background(), ReportRequestParams{
		Kind: ReportKindProgress, ManagedRunID: "managed-run_a", OperationID: "operation_report", ServiceReportID: "service-report_a", Summary: "progress",
	}); err != nil {
		t.Fatalf("valid report response: %v", err)
	}
}

func TestGeneratedClosedClientRejectsInvalidInputsAndResponses(t *testing.T) {
	transportFailure := errors.New("transport failed")
	tests := []struct {
		name   string
		invoke func() error
	}{
		{name: "nil handshake context", invoke: func() error {
			_, err := newClient(&recordingTransport{}).Handshake(missingContext(), HandshakeRequestParams{})
			return err
		}},
		{name: "changed handshake digest", invoke: func() error {
			_, err := newClient(&recordingTransport{}).Handshake(context.Background(), HandshakeRequestParams{ProtocolID: ProtocolID, BundleDigest: strings.Repeat("0", 64)})
			return err
		}},
		{name: "nil health context", invoke: func() error {
			_, err := newClient(&recordingTransport{}).Health(missingContext(), HealthRequestParams{})
			return err
		}},
		{name: "changed health digest", invoke: func() error {
			_, err := newClient(&recordingTransport{}).Health(context.Background(), HealthRequestParams{ProtocolID: ProtocolID, BundleDigest: strings.Repeat("0", 64)})
			return err
		}},
		{name: "health transport failure", invoke: func() error {
			_, err := newClient(&recordingTransport{err: transportFailure}).Health(context.Background(), HealthRequestParams{ProtocolID: ProtocolID, BundleDigest: BundleDigest, OperationID: "operation_health", ServiceInstanceID: "service-instance_a"})
			return err
		}},
		{name: "health response identity mismatch", invoke: func() error {
			transport := &recordingTransport{response: func(target any) { *(target.(*HealthResponse)) = HealthResponse{} }}
			_, err := newClient(transport).Health(context.Background(), HealthRequestParams{ProtocolID: ProtocolID, BundleDigest: BundleDigest, OperationID: "operation_health", ServiceInstanceID: "service-instance_a"})
			return err
		}},
		{name: "nil report context", invoke: func() error {
			_, err := newClient(&recordingTransport{}).Report(missingContext(), ReportRequestParams{})
			return err
		}},
		{name: "combined report content oversized", invoke: func() error {
			details := strings.Repeat("x", MaxReportBytes)
			_, err := newClient(&recordingTransport{}).Report(context.Background(), ReportRequestParams{Summary: "x", Details: &details})
			return err
		}},
		{name: "report transport failure", invoke: func() error {
			_, err := newClient(&recordingTransport{err: transportFailure}).Report(context.Background(), ReportRequestParams{Kind: ReportKindProgress, ManagedRunID: "managed-run_a", OperationID: "operation_report", ServiceReportID: "service-report_a", Summary: "progress"})
			return err
		}},
		{name: "report response identity mismatch", invoke: func() error {
			transport := &recordingTransport{response: func(target any) {
				*(target.(*ReportResponse)) = ReportResponse{JSONRPC: JSONRPCVersion, ID: "operation_report", Result: ReportResponseResult{AcceptedSequence: 1, ManagedRunID: "other", RetainedUntilMs: 1, ServiceReportID: "service-report_a"}}
			}}
			_, err := newClient(transport).Report(context.Background(), ReportRequestParams{Kind: ReportKindProgress, ManagedRunID: "managed-run_a", OperationID: "operation_report", ServiceReportID: "service-report_a", Summary: "progress"})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.invoke(); err == nil {
				t.Fatal("expected closed client rejection")
			}
		})
	}
}

func missingContext() context.Context { return nil }

func TestGeneratedErrorAndDiscriminatorContractsAreClosed(t *testing.T) {
	message := "safe message"
	failure := RPCError{Kind: ErrorKindInvalidRequest, Message: message}
	if failure.Error() != message || !failure.Kind.Valid() || ErrorKind("other").Valid() {
		t.Fatal("error kind contract is not closed")
	}
	for _, valid := range []interface{ Valid() bool }{
		AbandonDispositionReapSafe, AbandonReasonOwnerCancelled,
		AbandonTerminalTransitionUnboundPreparationAbandoned,
		CapabilityTerminalTransitionRunning, EvidenceDeliveryKindAttachment,
		EvidenceVerificationLevelAdapterVerified, ExecutionAttachmentKindUnixSocket,
		HealthStatusHealthy, ManagedRunStateActive, ReportKindProgress, ServiceScopeHealth,
	} {
		if !valid.Valid() {
			t.Fatal("generated discriminator rejected a defined value")
		}
	}
	for _, invalid := range []interface{ Valid() bool }{
		AbandonDisposition("invented"), AbandonReason("invented"), AbandonTerminalTransition("invented"),
		CapabilityTerminalTransition("invented"), EvidenceDeliveryKind("invented"),
		EvidenceVerificationLevel("invented"), ExecutionAttachmentKind("invented"),
		HealthStatus("invented"), ManagedRunState("invented"), ReportKind("invented"), ServiceScope("invented"),
	} {
		if invalid.Valid() {
			t.Fatal("generated discriminator accepted an undefined value")
		}
	}
	encoded, err := json.Marshal(failure)
	if err != nil || !strings.Contains(string(encoded), "invalid_request") {
		t.Fatalf("encode typed RPC error: %v", err)
	}
}
