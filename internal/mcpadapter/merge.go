package mcpadapter

import (
	"context"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mergeTool() *mcp.Tool {
	destructive, openWorld := true, true
	return &mcp.Tool{
		Name: ToolMergeTask,
		Description: "Merge one delivered task's exact approved pull-request head. " +
			"The repository, pull request, head, checks, credential, and method come from durable operator policy; " +
			"this call requires Comis approval metadata bound to the same managed operation.",
		Annotations: &mcp.ToolAnnotations{
			ReadOnlyHint: false, DestructiveHint: &destructive,
			IdempotentHint: true, OpenWorldHint: &openWorld,
		},
	}
}

func (facade *Facade) mergeTask(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input TaskInput,
) (*mcp.CallToolResult, MergeTaskOutput, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, MergeTaskOutput{}, err
	}
	if callContext.ApprovalRequestID == nil || callContext.ManagedRunID == nil {
		return nil, MergeTaskOutput{}, mergeApprovalFailure()
	}
	operationID := string(callContext.OperationID)
	approvalRequestID := string(*callContext.ApprovalRequestID)
	localInput := localapi.MergeTaskInput{
		TaskHandle: input.TaskHandle, ApprovalRequestID: approvalRequestID,
		MCPOperationID: operationID,
	}
	result, err := facade.client.MergeTask(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileMerge(ctx, operationID, localInput, err)
	}
	if err != nil {
		return nil, MergeTaskOutput{}, err
	}
	if !validMergeTaskResult(result, operationID, input.TaskHandle, approvalRequestID) {
		return nil, MergeTaskOutput{}, mergeResultFailure()
	}
	return nil, MergeTaskOutput{
		SchemaVersion: 1, OperationID: result.OperationID, TaskHandle: result.TaskHandle,
		State: result.State, RepositoryID: result.RepositoryID, PullRequestID: result.PullRequestID,
		HeadRevision: result.HeadRevision, ApprovalRequestID: result.ApprovalRequestID,
		ResolvingPrincipalID: result.ResolvingPrincipalID, MergeCommitRevision: result.MergeCommitRevision,
		Method: result.Method, CompletedAtMs: result.CompletedAt.UnixMilli(), StateVersion: result.StateVersion,
		SideEffect: localapi.SideEffectMutate,
	}, nil
}

// A merge retry is the reconciliation read: the durable merge transaction
// owns its consumed approval and post-forge receipt, while the generic operation
// ledger does not. Replaying the exact tuple can only resume that transaction or
// return its recorded completion; it cannot reserve a different task or head.
func (facade *Facade) reconcileMerge(
	ctx context.Context,
	operationID string,
	input localapi.MergeTaskInput,
	original error,
) (application.MergeTaskResult, error) {
	if ctx == nil {
		return application.MergeTaskResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	return facade.client.MergeTask(reconcileContext, operationID, input)
}

func validMergeTaskResult(result application.MergeTaskResult, operationID, taskHandle, approvalRequestID string) bool {
	return result.OperationID == operationID && result.TaskHandle == taskHandle &&
		result.State == application.TaskMergeCompleted && result.ApprovalRequestID == approvalRequestID &&
		domain.ValidateRepositoryID(result.RepositoryID) == nil &&
		domain.ValidateAuthorityReference("pullRequestId", result.PullRequestID) == nil &&
		domain.ValidateGitRevision(result.HeadRevision) == nil &&
		domain.ValidateGitRevision(result.MergeCommitRevision) == nil &&
		validMergePrincipal(result.ResolvingPrincipalID) && validMergeMethod(result.Method) &&
		!result.CompletedAt.IsZero() && result.CompletedAt.Location() == time.UTC && result.StateVersion > 0
}

func validMergePrincipal(value string) bool {
	return value != "" && len([]byte(value)) <= 256 && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validMergeMethod(method application.PullRequestMergeMethod) bool {
	return method == application.PullRequestMergeCommit || method == application.PullRequestMergeSquash ||
		method == application.PullRequestMergeRebase
}

func mergeApprovalFailure() error {
	return safeFailure(domain.ErrorPrecondition, false, "merge approval metadata is absent",
		"request approval for this exact managed merge operation")
}

func mergeResultFailure() error {
	return safeFailure(domain.ErrorInternal, false, "local merge result is invalid",
		"inspect durable merge and forge state before retrying")
}
