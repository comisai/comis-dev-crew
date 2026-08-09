package comiswire

import (
	"strings"
	"testing"
)

func TestPayloadValidationRejectsOpenTargetsMethodsAndBounds(t *testing.T) {
	unknown := PayloadTarget("other")
	if unknown.Valid() {
		t.Fatal("unknown payload target reported valid")
	}
	if err := ValidatePayload(unknown, []byte(`{}`)); err == nil {
		t.Fatal("expected unknown payload target rejection")
	}
	if _, err := CanonicalizePayload(unknown, []byte(`{}`)); err == nil {
		t.Fatal("expected unknown canonical target rejection")
	}
	if _, _, err := payloadContract(unknown, []byte(`{}`)); err == nil {
		t.Fatal("expected unknown payload contract rejection")
	}
	if err := ValidatePayload(PayloadRequest, []byte(strings.Repeat("x", MaxRequestBytes+1))); err == nil {
		t.Fatal("expected oversized request rejection")
	}
	unknownMethod := []byte(`{"jsonrpc":"2.0","id":"operation_a","method":"admin.call","params":{}}`)
	if err := ValidatePayload(PayloadRequest, unknownMethod); err == nil {
		t.Fatal("expected unknown method rejection")
	}
	malformed := []byte(`{"jsonrpc":"2.0","id":"operation_a","method":`)
	if _, _, err := requestContract(malformed); err == nil {
		t.Fatal("expected malformed request method rejection")
	}
}
