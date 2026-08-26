package localapi

import (
	"context"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func (handler *Handler) dispatchTaskMerge(
	ctx context.Context,
	caller CallerClass,
	request Request,
) (Outcome, bool) {
	if request.Method != MethodMergeTask {
		return Outcome{}, false
	}
	var input MergeTaskInput
	if err := decodeObject(request.Payload, &input); err != nil {
		return invalidPayload(request.OperationID, err), true
	}
	switch caller {
	case CallerOperatorCLI:
		if input.ApprovalRequestID != "" || input.MCPOperationID != "" {
			return invalidPayload(request.OperationID, nil), true
		}
	case CallerMCPFacade:
		if input.ApprovalRequestID == "" || input.MCPOperationID != request.OperationID {
			return invalidPayload(request.OperationID, nil), true
		}
	default:
		return rejectedOutcome(request.OperationID, domain.ErrorUnauthorized, false,
			"caller cannot use this method", "use the endpoint assigned to the caller class", nil), true
	}
	if handler.merges == nil {
		return rejectedOutcome(request.OperationID, domain.ErrorUnavailable, true,
			"task merge service is unavailable", "inspect service configuration", nil), true
	}
	result, err := handler.merges.MergeTask(ctx, application.MergeTaskCommand{
		OperationID: request.OperationID, TaskHandle: input.TaskHandle,
		ApprovalRequestID: input.ApprovalRequestID, MCPOperationID: input.MCPOperationID,
	})
	return taskMergeOutcome(request.OperationID, input, result, err), true
}

func taskMergeOutcome(
	operationID string,
	input MergeTaskInput,
	result application.MergeTaskResult,
	err error,
) Outcome {
	if err != nil {
		return outcomeFromError(operationID, err)
	}
	if !validTaskMergeResult(result, operationID, input.TaskHandle) {
		return rejectedOutcome(operationID, domain.ErrorInternal, false,
			"merge outcome is incomplete", "inspect durable service state", nil)
	}
	return queryOutcome(operationID, result.StateVersion, result, nil)
}

func validTaskMergeResult(result application.MergeTaskResult, operationID, taskHandle string) bool {
	if result.OperationID != operationID || result.TaskHandle != taskHandle || result.StateVersion < 1 ||
		domain.ValidateRepositoryID(result.RepositoryID) != nil ||
		domain.ValidateAuthorityReference("pullRequestId", result.PullRequestID) != nil ||
		domain.ValidateGitRevision(result.HeadRevision) != nil {
		return false
	}
	switch result.State {
	case application.TaskMergeAwaitingApproval:
		return result.ApprovalRequestID == "" && result.ResolvingPrincipalID == "" &&
			result.MergeCommitRevision == "" && result.Method == "" && result.CompletedAt.IsZero()
	case application.TaskMergeCompleted:
		return domain.ValidateAuthorityReference("approvalRequestId", result.ApprovalRequestID) == nil &&
			validMergePrincipalResult(result.ResolvingPrincipalID) &&
			domain.ValidateGitRevision(result.MergeCommitRevision) == nil && validMergeResultMethod(result.Method) &&
			!result.CompletedAt.IsZero() && result.CompletedAt.Location() == time.UTC
	default:
		return false
	}
}

func validMergePrincipalResult(value string) bool {
	return value != "" && len([]byte(value)) <= 256 && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func validMergeResultMethod(method application.PullRequestMergeMethod) bool {
	return method == application.PullRequestMergeCommit || method == application.PullRequestMergeSquash ||
		method == application.PullRequestMergeRebase
}
