package mcpadapter

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestFacadeCatalogIncludesCandidateApplicationMutation(t *testing.T) {
	facade, err := New(Config{
		Client: &fakeClient{}, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "operation-integration-mcp", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	tools, err := connectFacade(t, facade).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, listed := range tools.Tools {
		if listed.Name != "apply_integration_candidate" {
			continue
		}
		if listed.Annotations == nil || listed.Annotations.ReadOnlyHint ||
			listed.Annotations.DestructiveHint == nil || *listed.Annotations.DestructiveHint ||
			!listed.Annotations.IdempotentHint {
			t.Fatalf("integration tool annotations = %#v", listed.Annotations)
		}
		return
	}
	t.Fatal("apply_integration_candidate tool is absent")
}

func TestFacadeAppliesExactCandidateAndKeepsPolicyAndPathsPrivate(t *testing.T) {
	client := &integrationMCPClient{fakeClient: &fakeClient{}, result: integrationMCPResult()}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-integration-mcp", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, listed := range tools.Tools {
		if listed.Name != ToolApplyIntegration {
			continue
		}
		found = true
		encoded, marshalErr := json.Marshal(listed.InputSchema)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		schema := string(encoded)
		for _, required := range []string{"initiativeHandle", "integrationTaskHandle", "candidateTaskHandle", "candidateHead", "expectedIntegrationHead"} {
			if !strings.Contains(schema, required) {
				t.Fatalf("integration schema omits %q: %s", required, schema)
			}
		}
		for _, forbidden := range []string{"strategy", "policy", "worktree", "baseRevision", "argv"} {
			if strings.Contains(strings.ToLower(schema), strings.ToLower(forbidden)) {
				t.Fatalf("integration schema exposes %q: %s", forbidden, schema)
			}
		}
	}
	if !found {
		t.Fatal("integration tool is absent")
	}
	input := integrationMCPInput()
	called, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("operation-integration-mcp", "service-instance-0001"),
		Name: ToolApplyIntegration, Arguments: input,
	})
	if err != nil || called.IsError {
		t.Fatalf("CallTool(apply integration) = %#v, %v", called, err)
	}
	if client.input != input.local() || client.operationID != "operation-integration-mcp" {
		t.Fatalf("local integration call = %#v / %q", client.input, client.operationID)
	}
	visible, err := json.Marshal(called.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"worktree", "baseRevision", "/private/"} {
		if strings.Contains(string(visible), forbidden) {
			t.Fatalf("integration result exposes %q: %s", forbidden, visible)
		}
	}
}

func TestFacadeCandidateApplicationUsesSeparateConflictResolutionOperation(t *testing.T) {
	client := &integrationMCPClient{fakeClient: &fakeClient{}, result: integrationMCPResult()}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "generated-integration-operation", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	input := integrationMCPInput()
	arguments := map[string]any{
		"initiativeHandle": input.InitiativeHandle, "integrationTaskHandle": input.IntegrationTaskHandle,
		"candidateTaskHandle": input.CandidateTaskHandle, "candidateHead": input.CandidateHead,
		"expectedIntegrationHead": input.ExpectedIntegrationHead,
		"recoveryOperationId":     "failed-integration-operation",
	}
	called, err := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("new-integration-operation", "service-instance-0001"),
		Name: ToolApplyIntegration, Arguments: arguments,
	})
	if err != nil || called.IsError {
		t.Fatalf("CallTool(recover integration) = %#v, %v", called, err)
	}
	if client.operationID != "new-integration-operation" {
		t.Fatalf("resolution operation = %q, want new-integration-operation", client.operationID)
	}
	arguments["recoveryOperationId"] = "bad operation"
	refused, err := connectFacade(t, facade).CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("another-integration-operation", "service-instance-0001"),
		Name: ToolApplyIntegration, Arguments: arguments,
	})
	if err != nil || !refused.IsError || client.calls != 1 {
		t.Fatalf("CallTool(invalid recovery operation) = %#v, %v, calls=%d", refused, err, client.calls)
	}
}

func TestFacadeIntegrationApplicationRetriesOnlyUncertainExactCallAndValidatesResult(t *testing.T) {
	failure, err := domain.NewFailure(
		domain.ErrorUnavailable, true, "integration result is uncertain", "retry the exact operation", errors.New("transport closed"),
	)
	if err != nil {
		t.Fatal(err)
	}
	client := &integrationMCPClient{
		fakeClient: &fakeClient{}, result: integrationMCPResult(), errors: []error{failure, nil},
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001", Version: "test",
		NewOperationID: func() (string, error) { return "reconcile-integration-mcp", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{
		Meta: callMeta("operation-integration-mcp", "service-instance-0001"),
	}}
	_, result, err := facade.applyIntegrationCandidate(context.Background(), request, integrationMCPInput())
	if err != nil || result.Outcome != application.IntegrationApplied || client.calls != 2 {
		t.Fatalf("applyIntegrationCandidate(retry) = %#v, %v, calls=%d", result, err, client.calls)
	}
	client.errors = nil
	client.result.Strategy = application.IntegrationStrategy("reset")
	if _, _, err := facade.applyIntegrationCandidate(context.Background(), request, integrationMCPInput()); err == nil {
		t.Fatal("applyIntegrationCandidate(invalid result) error = nil")
	}
	if _, _, err := facade.applyIntegrationCandidate(context.Background(), &mcp.CallToolRequest{}, integrationMCPInput()); err == nil {
		t.Fatal("applyIntegrationCandidate(unauthorized) error = nil")
	}
	client.result = integrationMCPResult()
	client.errors = []error{errors.New("definitive integration failure")}
	if _, _, err := facade.applyIntegrationCandidate(context.Background(), request, integrationMCPInput()); err == nil {
		t.Fatal("applyIntegrationCandidate(definitive failure) error = nil")
	}
	original := errors.New("original integration failure")
	//lint:ignore SA1012 This boundary test proves uncertain integration recovery preserves the original result without a context.
	if _, err := facade.reconcileIntegrationApplication(nil, "operation-integration-mcp", localapi.ApplyIntegrationCandidateInput{}, original); !errors.Is(err, original) {
		t.Fatalf("reconcileIntegrationApplication(nil) error = %v", err)
	}
}

func TestIntegrationMCPOutcomeValidationCoversConflictsAndUnknownValues(t *testing.T) {
	conflicted := integrationMCPResult()
	conflicted.Outcome = application.IntegrationConflicted
	conflicted.ResultingHead = ""
	conflicted.ConflictPaths = []string{"a.txt", "b.txt"}
	if !validIntegrationMCPOutcome(conflicted) {
		t.Fatal("valid conflicted outcome was rejected")
	}
	invalidated := integrationMCPResult()
	invalidated.Outcome = application.IntegrationInvalidated
	invalidated.ResultingHead = ""
	if !validIntegrationMCPOutcome(invalidated) {
		t.Fatal("valid invalidated outcome was rejected")
	}
	unknown := integrationMCPResult()
	unknown.Outcome = application.IntegrationOutcome("unknown")
	if validIntegrationMCPOutcome(unknown) {
		t.Fatal("unknown integration outcome was accepted")
	}
	for _, paths := range [][]string{nil, {"z.txt", "a.txt"}, {"a.txt", "a.txt"}, {"../escape"}} {
		if validIntegrationMCPConflictPaths(paths) {
			t.Fatalf("invalid MCP conflict paths were accepted: %#v", paths)
		}
	}
}

func integrationMCPInput() ApplyIntegrationCandidateInput {
	return ApplyIntegrationCandidateInput{
		InitiativeHandle: "initiative-mcp", IntegrationTaskHandle: "task-integration",
		CandidateTaskHandle: "task-candidate", CandidateHead: strings.Repeat("b", 40),
		ExpectedIntegrationHead: strings.Repeat("c", 40),
	}
}

func integrationMCPResult() localapi.ApplyIntegrationCandidateResult {
	return localapi.ApplyIntegrationCandidateResult{
		SchemaVersion: 1, OperationID: "operation-integration-mcp",
		InitiativeHandle: "initiative-mcp", IntegrationTaskHandle: "task-integration",
		CandidateTaskHandle: "task-candidate", RepositoryID: "repo-primary",
		CandidateHead: strings.Repeat("b", 40), EvidenceDigest: strings.Repeat("e", 64),
		Strategy: application.IntegrationMerge, Outcome: application.IntegrationApplied,
		PreviousHead: strings.Repeat("c", 40), ResultingHead: strings.Repeat("d", 40),
		StateVersion: 41, CompletedAtMs: time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC).UnixMilli(),
		SideEffect: localapi.SideEffectMutate,
	}
}

type integrationMCPClient struct {
	*fakeClient
	result      localapi.ApplyIntegrationCandidateResult
	input       localapi.ApplyIntegrationCandidateInput
	operationID string
	errors      []error
	calls       int
}

func (client *integrationMCPClient) ApplyIntegrationCandidate(
	_ context.Context,
	operationID string,
	input localapi.ApplyIntegrationCandidateInput,
) (localapi.ApplyIntegrationCandidateResult, error) {
	client.calls++
	client.operationID = operationID
	client.input = input
	client.result.OperationID = operationID
	client.result.RecoveryOperationID = input.RecoveryOperationID
	if len(client.errors) == 0 {
		return client.result, nil
	}
	err := client.errors[0]
	client.errors = client.errors[1:]
	return client.result, err
}
