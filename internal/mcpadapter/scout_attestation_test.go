package mcpadapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const attestToolName = "attest_scout_decisions"

// Only a model can inventory decisions from prose, so the liaison is the one
// actor that can record this — which means it has to be reachable as a tool. The
// schema must make the finding a stated choice, because an inventory that could
// be satisfied by omission would let a buried question pass as "nothing open".
func TestFacade_AttestationRequiresAStatedFinding(t *testing.T) {
	client := &fakeClient{attestResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
		SideEffect: localapi.SideEffectMutate,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
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
	var found bool
	for _, tool := range tools.Tools {
		if tool.Name != attestToolName {
			continue
		}
		found = true
		schema, marshalErr := json.Marshal(tool.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		visible := tool.Description + "\n" + string(schema)
		for _, required := range []string{"open_decisions", "no_open_decisions", "openDecisionKeys"} {
			if !strings.Contains(visible, required) {
				t.Errorf("%s contract omits %q: %s", attestToolName, required, visible)
			}
		}
		// A model choosing this tool needs to know what it unblocks, or it will
		// never think to call it before asking for cleanup.
		if !strings.Contains(strings.ToLower(tool.Description), "cleanup") {
			t.Errorf("%s does not say what it unblocks: %s", attestToolName, tool.Description)
		}
	}
	if !found {
		t.Fatalf("%s tool is absent", attestToolName)
	}
}

// The finding and its keys reach the canonical command exactly as stated. The
// adapter forwards rather than re-deciding: the coordinator owns whether an
// inventory is complete, and a second judgement here could disagree with the
// one that actually refuses.
func TestFacade_AttestationForwardsTheStatedInventory(t *testing.T) {
	client := &fakeClient{attestResult: localapi.TaskMutationResult{
		TaskHandle: "task-0001", State: domain.TaskWorking, StateVersion: 9,
		SideEffect: localapi.SideEffectMutate,
	}}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)

	for label, expected := range map[string]struct {
		arguments map[string]any
		want      string
	}{
		"open decisions": {
			map[string]any{
				"taskHandle": "task-0001", "finding": "open_decisions",
				"openDecisionKeys": []string{"schema-choice", "rollout-window"},
			},
			"attest:attest-0001:task-0001:open_decisions:schema-choice,rollout-window",
		},
		"none open": {
			map[string]any{
				"taskHandle": "task-0001", "finding": "no_open_decisions",
				"openDecisionKeys": []string{},
			},
			"attest:attest-0001:task-0001:no_open_decisions:",
		},
	} {
		client.calls = nil
		result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{
			Meta: callMeta("attest-0001", "service-instance-0001"),
			Name: attestToolName, Arguments: expected.arguments,
		})
		if callErr != nil || result.IsError {
			t.Fatalf("CallTool(%s, %s) = %#v, %v", attestToolName, label, result, callErr)
		}
		if len(client.calls) != 1 || client.calls[0] != expected.want {
			t.Fatalf("%s calls = %v, want %q", label, client.calls, expected.want)
		}
	}
}

func (client *fakeClient) AttestScoutDecisions(
	_ context.Context,
	operationID string,
	input localapi.AttestScoutDecisionsInput,
) (localapi.TaskMutationResult, error) {
	client.calls = append(
		client.calls,
		"attest:"+operationID+":"+input.TaskHandle+":"+string(input.Finding)+":"+
			strings.Join(input.OpenDecisionKeys, ","),
	)
	if len(client.attestErrors) == 0 {
		return client.attestResult, nil
	}
	failure := client.attestErrors[0]
	client.attestErrors = client.attestErrors[1:]
	return localapi.TaskMutationResult{}, failure
}
