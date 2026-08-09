package conformance_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
)

type fixtureScenario struct {
	Class string        `json:"class"`
	Name  string        `json:"name"`
	Steps []fixtureStep `json:"steps"`
}

type fixtureStep struct {
	Expectation       string                  `json:"expectation"`
	ExpectedErrorKind *comiswire.ErrorKind    `json:"expectedErrorKind,omitempty"`
	Payload           json.RawMessage         `json:"payload"`
	SchemaExpectation string                  `json:"schemaExpectation"`
	Target            comiswire.PayloadTarget `json:"target"`
}

type semanticBoundary struct {
	operations map[string]string
}

func TestGeneratedBoundaryMatchesEveryPinnedFixtureStep(t *testing.T) {
	for _, scenario := range loadFixtureScenarios(t) {
		t.Run(scenario.Class, func(t *testing.T) {
			boundary := semanticBoundary{operations: make(map[string]string)}
			for index, step := range scenario.Steps {
				payload := materializeFixturePayload(t, step.Payload)
				schemaErr := comiswire.ValidatePayload(step.Target, payload)
				schemaAccepted := schemaErr == nil
				if schemaAccepted != (step.SchemaExpectation == "accept") {
					t.Fatalf("step %d schema acceptance = %v, want %s: %v", index, schemaAccepted, step.SchemaExpectation, schemaErr)
				}
				kind := boundary.validate(step.Target, payload)
				accepted := kind == nil
				if accepted != (step.Expectation == "accept") {
					t.Fatalf("step %d semantic acceptance = %v, want %s (%v)", index, accepted, step.Expectation, kind)
				}
				if kind != nil && (step.ExpectedErrorKind == nil || *kind != *step.ExpectedErrorKind) {
					t.Fatalf("step %d error kind = %q, want %v", index, *kind, step.ExpectedErrorKind)
				}
			}
		})
	}
}

func TestCanonicalFixtureExamplesRoundTripByteExactly(t *testing.T) {
	var valid fixtureScenario
	for _, scenario := range loadFixtureScenarios(t) {
		if scenario.Class == "valid" {
			valid = scenario
			break
		}
	}
	if len(valid.Steps) == 0 {
		t.Fatal("valid canonical fixture is absent")
	}
	for index, step := range valid.Steps {
		payload := materializeFixturePayload(t, step.Payload)
		canonical, err := comiswire.CanonicalizePayload(step.Target, payload)
		if err != nil {
			t.Fatalf("canonicalize step %d: %v", index, err)
		}
		if !bytes.Equal(canonical, payload) {
			t.Fatalf("canonical step %d changed bytes\n got: %s\nwant: %s", index, canonical, payload)
		}
	}
}

func TestSemanticBoundaryAcceptsIdenticalReplayAndRejectsAlteration(t *testing.T) {
	scenario := fixtureByClass(t, "altered-replay")
	if len(scenario.Steps) != 2 {
		t.Fatalf("altered replay step count = %d", len(scenario.Steps))
	}
	boundary := semanticBoundary{operations: make(map[string]string)}
	original := materializeFixturePayload(t, scenario.Steps[0].Payload)
	altered := materializeFixturePayload(t, scenario.Steps[1].Payload)
	if kind := boundary.validate(comiswire.PayloadRequest, original); kind != nil {
		t.Fatalf("original request rejected: %q", *kind)
	}
	if kind := boundary.validate(comiswire.PayloadRequest, original); kind != nil {
		t.Fatalf("identical replay rejected: %q", *kind)
	}
	kind := boundary.validate(comiswire.PayloadRequest, altered)
	if kind == nil || *kind != comiswire.ErrorKindReplayConflict {
		t.Fatalf("altered replay kind = %v", kind)
	}
}

func TestSemanticBoundaryCountsCombinedUTF8ReportBytes(t *testing.T) {
	boundaryFixture := fixtureByClass(t, "boundary-size")
	if len(boundaryFixture.Steps) == 0 {
		t.Fatal("boundary-size fixture is empty")
	}
	base := decodeObject(t, materializeFixturePayload(t, boundaryFixture.Steps[0].Payload))
	params := base["params"].(map[string]any)
	params["summary"] = strings.Repeat("é", comiswire.MaxReportBytes)
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("encode UTF-8 report: %v", err)
	}
	boundary := semanticBoundary{operations: make(map[string]string)}
	kind := boundary.validate(comiswire.PayloadRequest, encoded)
	if kind == nil || *kind != comiswire.ErrorKindSizeLimitExceeded {
		t.Fatalf("UTF-8 report rejection = %v", kind)
	}

	params["summary"] = strings.Repeat("x", comiswire.MaxReportBytes/2)
	params["details"] = strings.Repeat("y", comiswire.MaxReportBytes/2+1)
	encoded, err = json.Marshal(base)
	if err != nil {
		t.Fatalf("encode combined report: %v", err)
	}
	kind = boundary.validate(comiswire.PayloadRequest, encoded)
	if kind == nil || *kind != comiswire.ErrorKindSizeLimitExceeded {
		t.Fatalf("combined report rejection = %v", kind)
	}
}

func TestSemanticBoundaryRejectsEnvelopeOperationDisagreement(t *testing.T) {
	valid := fixtureByClass(t, "valid")
	var report []byte
	for _, step := range valid.Steps {
		payload := materializeFixturePayload(t, step.Payload)
		object := decodeObject(t, payload)
		if object["method"] == string(comiswire.MethodManagedRunsReport) {
			report = payload
			break
		}
	}
	if report == nil {
		t.Fatal("valid report request is absent")
	}
	object := decodeObject(t, report)
	object["id"] = "operation_other"
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatalf("encode altered envelope: %v", err)
	}
	boundary := semanticBoundary{operations: make(map[string]string)}
	kind := boundary.validate(comiswire.PayloadRequest, encoded)
	if kind == nil || *kind != comiswire.ErrorKindInvalidRequest {
		t.Fatalf("operation disagreement rejection = %v", kind)
	}
}

func TestSemanticBoundaryRejectsWorkspacePreparationActivatedWithoutLease(t *testing.T) {
	boundary := semanticBoundary{operations: make(map[string]string)}
	preparation := []byte(`{"state":"prepared","externalRunRef":"external-run_workspace","registrationNonce":"registration-nonce_workspace","expiresAt":"2030-01-01T00:00:00.000Z","requestedWorkspace":{"rootHint":"/approved/workspaces/task"}}`)
	if kind := boundary.validate(comiswire.PayloadMCPManagedRunResult, preparation); kind != nil {
		t.Fatalf("workspace preparation rejected: %q", *kind)
	}
	activation := []byte(`{"jsonrpc":"2.0","id":"operation_workspace","method":"managedRuns.activate","params":{"operationId":"operation_workspace","managedRunId":"managed-run_workspace","externalRunRef":"external-run_workspace","registrationNonce":"registration-nonce_workspace"}}`)
	kind := boundary.validate(comiswire.PayloadRequest, activation)
	if kind == nil || *kind != comiswire.ErrorKindInvalidParams {
		t.Fatalf("missing workspace lease rejection = %v", kind)
	}
}

func (boundary *semanticBoundary) validate(target comiswire.PayloadTarget, payload []byte) *comiswire.ErrorKind {
	if target != comiswire.PayloadMCPCallContext && target != comiswire.PayloadMCPManagedRunResult {
		limit := comiswire.MaxResponseBytes
		if target == comiswire.PayloadRequest {
			limit = comiswire.MaxRequestBytes
		}
		if len(payload) > limit || len(payload)+1 > comiswire.MaxLineBytes {
			return errorKind(comiswire.ErrorKindSizeLimitExceeded)
		}
	}
	if target != comiswire.PayloadRequest {
		if err := comiswire.ValidatePayload(target, payload); err != nil {
			return errorKind(comiswire.ErrorKindInvalidParams)
		}
		return nil
	}
	envelope := decodeObjectValue(payload)
	params, ok := envelope["params"].(map[string]any)
	if envelope == nil || !ok {
		return errorKind(comiswire.ErrorKindInvalidRequest)
	}
	method, ok := envelope["method"].(string)
	if !ok || !knownMethod(method) {
		return errorKind(comiswire.ErrorKindMethodNotFound)
	}
	if protocolID, exists := params["protocolId"]; exists && protocolID != comiswire.ProtocolID {
		return errorKind(comiswire.ErrorKindProtocolMismatch)
	}
	if digest, exists := params["bundleDigest"]; exists && digest != comiswire.BundleDigest {
		return errorKind(comiswire.ErrorKindBundleDigestMismatch)
	}
	if method == string(comiswire.MethodManagedRunsReport) {
		summary, _ := params["summary"].(string)
		details, _ := params["details"].(string)
		if len([]byte(summary))+len([]byte(details)) > comiswire.MaxReportBytes {
			return errorKind(comiswire.ErrorKindSizeLimitExceeded)
		}
	}
	if err := comiswire.ValidatePayload(target, payload); err != nil {
		return errorKind(comiswire.ErrorKindInvalidParams)
	}
	id, idOK := envelope["id"].(string)
	operationID, operationOK := params["operationId"].(string)
	if !idOK || !operationOK || id != operationID {
		return errorKind(comiswire.ErrorKindInvalidRequest)
	}
	canonical, err := comiswire.CanonicalizePayload(target, payload)
	if err != nil {
		return errorKind(comiswire.ErrorKindInvalidParams)
	}
	if previous, exists := boundary.operations[operationID]; exists && previous != string(canonical) {
		return errorKind(comiswire.ErrorKindReplayConflict)
	}
	boundary.operations[operationID] = string(canonical)
	return nil
}

func loadFixtureScenarios(t *testing.T) []fixtureScenario {
	t.Helper()
	root := conformanceProtocolRoot(t)
	names, err := filepath.Glob(filepath.Join(root, "fixtures", "*.json"))
	if err != nil {
		t.Fatalf("list fixture scenarios: %v", err)
	}
	sort.Strings(names)
	scenarios := make([]fixtureScenario, 0, len(names))
	for _, name := range names {
		contents, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read fixture %s: %v", filepath.Base(name), err)
		}
		decoder := json.NewDecoder(bytes.NewReader(contents))
		decoder.DisallowUnknownFields()
		var scenario fixtureScenario
		if err := decoder.Decode(&scenario); err != nil {
			t.Fatalf("decode fixture %s: %v", filepath.Base(name), err)
		}
		if scenario.Class == "" || scenario.Name == "" || len(scenario.Steps) == 0 {
			t.Fatalf("fixture %s is incomplete", filepath.Base(name))
		}
		for _, step := range scenario.Steps {
			if !step.Target.Valid() || (step.Expectation != "accept" && step.Expectation != "reject") || (step.SchemaExpectation != "accept" && step.SchemaExpectation != "reject") {
				t.Fatalf("fixture %s has an open discriminator", filepath.Base(name))
			}
		}
		scenarios = append(scenarios, scenario)
	}
	return scenarios
}

func materializeFixturePayload(t *testing.T, payload json.RawMessage) []byte {
	t.Helper()
	var value any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		t.Fatalf("decode fixture payload: %v", err)
	}
	value = replaceDigestToken(value)
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode materialized fixture payload: %v", err)
	}
	return encoded
}

func replaceDigestToken(value any) any {
	switch typed := value.(type) {
	case string:
		if typed == "__BUNDLE_DIGEST__" {
			return comiswire.BundleDigest
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = replaceDigestToken(item)
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = replaceDigestToken(item)
		}
		return typed
	default:
		return typed
	}
}

func fixtureByClass(t *testing.T, class string) fixtureScenario {
	t.Helper()
	for _, scenario := range loadFixtureScenarios(t) {
		if scenario.Class == class {
			return scenario
		}
	}
	t.Fatalf("fixture class %q is absent", class)
	return fixtureScenario{}
}

func decodeObject(t *testing.T, payload []byte) map[string]any {
	t.Helper()
	object := decodeObjectValue(payload)
	if object == nil {
		t.Fatalf("payload is not an object: %s", payload)
	}
	return object
}

func decodeObjectValue(payload []byte) map[string]any {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil
	}
	return object
}

func knownMethod(method string) bool {
	switch comiswire.Method(method) {
	case comiswire.MethodCapabilityServicesHandshake, comiswire.MethodCapabilityServicesHealth, comiswire.MethodManagedRunsAbandon, comiswire.MethodManagedRunsActivate, comiswire.MethodManagedRunsReport:
		return true
	default:
		return false
	}
}

func errorKind(kind comiswire.ErrorKind) *comiswire.ErrorKind { return &kind }

func conformanceProtocolRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve conformance test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "protocol", "comis"))
}
