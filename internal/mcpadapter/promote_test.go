package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A model must not be able to aim a promotion at code the investigation never
// covered. The repository and base revision are inherited from the scout, so
// the input type has no field for either — a schema that offered them would
// invite exactly the substitution the inheritance exists to prevent.
func TestFacade_PromoteScoutInputCannotChooseTheGroundItStartsFrom(t *testing.T) {
	input := reflect.TypeOf(PromoteScoutInput{})
	for _, forbidden := range []string{"RepositoryID", "BaseRevision", "Shape"} {
		if _, found := input.FieldByName(forbidden); found {
			t.Errorf("promote_scout input exposes %q, which is inherited from the scout", forbidden)
		}
	}
	if _, found := input.FieldByName("ScoutTaskHandle"); !found {
		t.Error("promote_scout input must name the scout it promotes")
	}
}

func TestFacade_PromoteScoutMintsATaskAndPreservesTheScout(t *testing.T) {
	promoted := preparedResult()
	promoted.OperationID = "promote-0001"
	client := &fakeClient{promoteResult: promoted}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("promote-0001", "service-instance-0001"),
		Name: ToolPromoteScout,
		Arguments: PromoteScoutInput{
			ScoutTaskHandle:    "task-scout-0001",
			AcceptanceCriteria: []string{"The investigated change is implemented and proven."},
			Constraints:        []string{"Preserve unrelated changes."},
			ValidationProfile:  "go-default", DeliveryMode: "pull_request",
			WorkerProfileID: "codex-reviewed",
		},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(promote_scout) = %#v, %v", result, callErr)
	}
	if len(client.calls) != 1 || client.calls[0] != "promote:promote-0001:task-scout-0001" {
		t.Fatalf("promotion did not route through one canonical command: %v", client.calls)
	}
	// Registration data stays private, exactly as it does for preparation: the
	// visible result names the new task, never the nonce that could activate it.
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal promotion output: %v", err)
	}
	if strings.Contains(string(encoded), "registration-nonce_private") {
		t.Errorf("promotion leaked private registration data: %s", encoded)
	}
	if result.Meta[ManagedRunResultMetaKey] == nil {
		t.Error("promotion must return its private managed-run metadata")
	}
}

func TestFacade_PromoteScoutDeclaresWhatItInherits(t *testing.T) {
	facade, err := New(Config{
		Client: &fakeClient{}, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}
	for _, tool := range tools.Tools {
		if tool.Name != ToolPromoteScout {
			continue
		}
		description := strings.ToLower(tool.Description)
		for _, required := range []string{"inherited from the scout", "preserving the scout"} {
			if !strings.Contains(description, required) {
				t.Errorf("promote_scout description omits %q: %s", required, tool.Description)
			}
		}
		return
	}
	t.Fatal("promote_scout tool is absent")
}

// An uncertain promotion is replayed through PromoteScout, not PrepareTask. The
// prepared half replays to the same task either way, but only the promotion path
// re-attempts the scout link — which may be exactly the half that never ran, and
// whose absence would leave a ship task carrying a scout's conclusions with
// nothing recording where they came from.
func TestFacade_AnUncertainPromotionIsReplayedAsAPromotionNotAPreparation(t *testing.T) {
	promoted := preparedResult()
	promoted.OperationID = "promote-0001"
	client := &fakeClient{
		promoteErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		promoteResult: promoted,
		operation:     fixtureOperation("promote-0001", "PrepareTask"),
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("promote-0001", "service-instance-0001"),
		Name: ToolPromoteScout,
		Arguments: PromoteScoutInput{
			ScoutTaskHandle: "task-scout-0001", AcceptanceCriteria: []string{"x"},
			Constraints: []string{}, ValidationProfile: "go-default",
			DeliveryMode: "pull_request", WorkerProfileID: "codex-reviewed",
		},
	})
	if callErr != nil || result.IsError {
		t.Fatalf("CallTool(promote_scout) = %#v, %v", result, callErr)
	}
	var promoteCalls int
	for _, call := range client.calls {
		if strings.HasPrefix(call, "promote:") {
			promoteCalls++
		}
		if strings.HasPrefix(call, "prepare:") {
			t.Fatalf("an uncertain promotion must not replay as a preparation: %v", client.calls)
		}
	}
	if promoteCalls != 2 {
		t.Fatalf("expected the promotion to be replayed once, got calls %v", client.calls)
	}
}

// A promotion whose operation was never recorded stays uncertain. Replaying it
// blind could mint a second ship task from the same investigation.
func TestFacade_APromotionWithNoRecordedOperationStaysUncertain(t *testing.T) {
	client := &fakeClient{
		promoteErrors: []error{&domain.Failure{
			Code: domain.ErrorUnavailable, Retryable: true, Message: "send uncertain",
		}},
		operationError: errors.New("operation unavailable"),
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("promote-0001", "service-instance-0001"),
		Name: ToolPromoteScout,
		Arguments: PromoteScoutInput{
			ScoutTaskHandle: "task-scout-0001", AcceptanceCriteria: []string{"x"},
			Constraints: []string{}, ValidationProfile: "go-default",
			DeliveryMode: "pull_request", WorkerProfileID: "codex-reviewed",
		},
	})
	if callErr == nil && (result == nil || !result.IsError) {
		t.Fatalf("CallTool(promote_scout) = %#v, %v, want the original uncertainty", result, callErr)
	}
}
