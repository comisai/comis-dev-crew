package localapi

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskMergeMethodRequiresCanonicalMutationSurface(t *testing.T) {
	method := Method("MergeTask")
	if !method.valid() || method.SideEffect() != SideEffectMutate ||
		!methodAllowed(CallerOperatorCLI, method) || !methodAllowed(CallerMCPFacade, method) {
		t.Fatalf("merge method posture = valid:%t sideEffect:%q operator:%t mcp:%t",
			method.valid(), method.SideEffect(), methodAllowed(CallerOperatorCLI, method), methodAllowed(CallerMCPFacade, method))
	}
	handler := newTestHandler(t, nil)
	outcome := handler.handle(context.Background(), CallerOperatorCLI, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-merge-api",`+
			`"method":"MergeTask","payload":{"taskHandle":"task-merge-api"}}`,
	))
	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable || !outcome.Error.Retryable {
		t.Fatalf("absent merge surface outcome = %#v", outcome)
	}
}

func TestTaskMergeServerClientBindsApprovalMetadataToEndpointClass(t *testing.T) {
	pendingSurface := &apiTaskMerges{result: mergeAPIResult(
		"operation-merge-operator", application.TaskMergeAwaitingApproval,
	)}
	operatorHandler := newTaskMergeHandler(t, pendingSurface)
	operatorClient, err := NewClient(startHandlerServer(t, operatorHandler, CallerOperatorCLI), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := operatorClient.MergeTask(context.Background(), "operation-merge-operator", MergeTaskInput{
		TaskHandle: "task-merge-api",
	})
	if err != nil || pending.State != application.TaskMergeAwaitingApproval || pending.StateVersion != 12 {
		t.Fatalf("MergeTask(operator) = %#v, %v", pending, err)
	}
	wantOperator := application.MergeTaskCommand{
		OperationID: "operation-merge-operator", TaskHandle: "task-merge-api",
	}
	if !reflect.DeepEqual(pendingSurface.command, wantOperator) {
		t.Fatalf("operator merge command = %#v, want %#v", pendingSurface.command, wantOperator)
	}

	completedSurface := &apiTaskMerges{result: mergeAPIResult(
		"operation-merge-mcp", application.TaskMergeCompleted,
	)}
	mcpHandler := newTaskMergeHandler(t, completedSurface)
	mcpClient, err := NewClient(startHandlerServer(t, mcpHandler, CallerMCPFacade), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := mcpClient.MergeTask(context.Background(), "operation-merge-mcp", MergeTaskInput{
		TaskHandle: "task-merge-api", ApprovalRequestID: "approval-request-merge",
		MCPOperationID: "operation-merge-mcp",
	})
	if err != nil || completed.State != application.TaskMergeCompleted ||
		completed.MergeCommitRevision != strings.Repeat("c", 40) || completed.Method != application.PullRequestMergeSquash {
		t.Fatalf("MergeTask(MCP) = %#v, %v", completed, err)
	}
	wantMCP := application.MergeTaskCommand{
		OperationID: "operation-merge-mcp", TaskHandle: "task-merge-api",
		ApprovalRequestID: "approval-request-merge", MCPOperationID: "operation-merge-mcp",
	}
	if !reflect.DeepEqual(completedSurface.command, wantMCP) {
		t.Fatalf("MCP merge command = %#v, want %#v", completedSurface.command, wantMCP)
	}
}

func TestTaskMergeBoundaryRejectsForgedAuthorityAndIncompleteResults(t *testing.T) {
	surface := &apiTaskMerges{result: mergeAPIResult("operation-merge-boundary", application.TaskMergeCompleted)}
	handler := newTaskMergeHandler(t, surface)
	operatorWithApproval := handler.handle(context.Background(), CallerOperatorCLI, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-merge-boundary",`+
			`"method":"MergeTask","payload":{"taskHandle":"task-merge-api",`+
			`"approvalRequestId":"approval-request-merge","mcpOperationId":"operation-merge-boundary"}}`,
	))
	if operatorWithApproval.Error == nil || operatorWithApproval.Error.Code != domain.ErrorInvalidArgument {
		t.Fatalf("operator approval metadata outcome = %#v", operatorWithApproval)
	}
	mcpWithoutApproval := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-merge-boundary",`+
			`"method":"MergeTask","payload":{"taskHandle":"task-merge-api"}}`,
	))
	if mcpWithoutApproval.Error == nil || mcpWithoutApproval.Error.Code != domain.ErrorInvalidArgument {
		t.Fatalf("MCP missing approval metadata outcome = %#v", mcpWithoutApproval)
	}
	forgedForgeTarget := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-merge-boundary",`+
			`"method":"MergeTask","payload":{"taskHandle":"task-merge-api",`+
			`"approvalRequestId":"approval-request-merge","mcpOperationId":"operation-merge-boundary",`+
			`"repositoryId":"forged"}}`,
	))
	if forgedForgeTarget.Error == nil || forgedForgeTarget.Error.Code != domain.ErrorInvalidArgument || surface.calls != 0 {
		t.Fatalf("forged merge authority outcome/calls = %#v/%d", forgedForgeTarget, surface.calls)
	}

	surface.result.PullRequestID = ""
	incomplete := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-merge-boundary",`+
			`"method":"MergeTask","payload":{"taskHandle":"task-merge-api",`+
			`"approvalRequestId":"approval-request-merge","mcpOperationId":"operation-merge-boundary"}}`,
	))
	if incomplete.Error == nil || incomplete.Error.Code != domain.ErrorInternal || surface.calls != 1 {
		t.Fatalf("incomplete merge outcome/calls = %#v/%d", incomplete, surface.calls)
	}
}

type apiTaskMerges struct {
	command application.MergeTaskCommand
	result  application.MergeTaskResult
	calls   int
}

func (merges *apiTaskMerges) MergeTask(
	_ context.Context,
	command application.MergeTaskCommand,
) (application.MergeTaskResult, error) {
	merges.command = command
	merges.calls++
	return merges.result, nil
}

func newTaskMergeHandler(t *testing.T, merges TaskMerges) *Handler {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Merges: merges,
		ServiceInstanceID: "service-instance-api", Clock: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func mergeAPIResult(operationID string, state application.TaskMergeState) application.MergeTaskResult {
	result := application.MergeTaskResult{
		OperationID: operationID, TaskHandle: "task-merge-api", State: state,
		RepositoryID: "repo-api", PullRequestID: "pull-request-merge",
		HeadRevision: strings.Repeat("a", 40), StateVersion: 12,
	}
	if state == application.TaskMergeCompleted {
		result.ApprovalRequestID = "approval-request-merge"
		result.ResolvingPrincipalID = "operator_a"
		result.MergeCommitRevision = strings.Repeat("c", 40)
		result.Method = application.PullRequestMergeSquash
		result.CompletedAt = time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC)
	}
	return result
}
