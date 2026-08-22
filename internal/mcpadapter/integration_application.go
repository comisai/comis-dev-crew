package mcpadapter

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ApplyIntegrationCandidateInput names one exact candidate and expected target
// head. Strategy, policy, worktrees, and Git argv remain operator-owned.
type ApplyIntegrationCandidateInput struct {
	InitiativeHandle        string `json:"initiativeHandle" jsonschema:"opaque initiative handle"`
	IntegrationTaskHandle   string `json:"integrationTaskHandle" jsonschema:"opaque handle of the initiative's dedicated integration owner task"`
	CandidateTaskHandle     string `json:"candidateTaskHandle" jsonschema:"opaque handle of one accepted component task"`
	CandidateHead           string `json:"candidateHead" jsonschema:"exact accepted 40-character lowercase hexadecimal candidate revision"`
	ExpectedIntegrationHead string `json:"expectedIntegrationHead" jsonschema:"exact current 40-character lowercase hexadecimal integration revision"`
	RecoveryOperationID     string `json:"recoveryOperationId,omitempty" jsonschema:"exact failed integration operation identity to resume; omit for a new application"`
}

func (input ApplyIntegrationCandidateInput) local() localapi.ApplyIntegrationCandidateInput {
	return localapi.ApplyIntegrationCandidateInput{
		InitiativeHandle: input.InitiativeHandle, IntegrationTaskHandle: input.IntegrationTaskHandle,
		CandidateTaskHandle: input.CandidateTaskHandle, CandidateHead: input.CandidateHead,
		ExpectedIntegrationHead: input.ExpectedIntegrationHead,
	}
}

func (facade *Facade) applyIntegrationCandidate(
	ctx context.Context,
	request *mcp.CallToolRequest,
	input ApplyIntegrationCandidateInput,
) (*mcp.CallToolResult, localapi.ApplyIntegrationCandidateResult, error) {
	callContext, err := facade.authorize(request)
	if err != nil {
		return nil, localapi.ApplyIntegrationCandidateResult{}, err
	}
	operationID := string(callContext.OperationID)
	if input.RecoveryOperationID != "" {
		if domain.ValidateOperationID(input.RecoveryOperationID) != nil {
			return nil, localapi.ApplyIntegrationCandidateResult{}, invalidOperationFailure()
		}
		operationID = input.RecoveryOperationID
	}
	localInput := input.local()
	result, err := facade.client.ApplyIntegrationCandidate(ctx, operationID, localInput)
	if err != nil && uncertainMutation(ctx, err) {
		result, err = facade.reconcileIntegrationApplication(ctx, operationID, localInput, err)
	}
	if err != nil {
		return nil, localapi.ApplyIntegrationCandidateResult{}, err
	}
	if result.SchemaVersion != 1 || result.OperationID != operationID ||
		result.InitiativeHandle != input.InitiativeHandle || result.IntegrationTaskHandle != input.IntegrationTaskHandle ||
		result.CandidateTaskHandle != input.CandidateTaskHandle || result.CandidateHead != input.CandidateHead ||
		result.PreviousHead != input.ExpectedIntegrationHead || domain.ValidateRepositoryID(result.RepositoryID) != nil ||
		domain.ValidateBriefRevisionHash(result.EvidenceDigest) != nil || result.CompletedAtMs <= 0 || result.StateVersion < 1 ||
		result.SideEffect != localapi.SideEffectMutate || !validIntegrationMCPOutcome(result) {
		return nil, localapi.ApplyIntegrationCandidateResult{}, internalResultFailure()
	}
	result.ConflictPaths = append([]string(nil), result.ConflictPaths...)
	return nil, result, nil
}

func (facade *Facade) reconcileIntegrationApplication(
	ctx context.Context,
	operationID string,
	input localapi.ApplyIntegrationCandidateInput,
	original error,
) (localapi.ApplyIntegrationCandidateResult, error) {
	if ctx == nil {
		return localapi.ApplyIntegrationCandidateResult{}, original
	}
	reconcileContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), facade.reconcileTimeout)
	defer cancel()
	// A reserved integration call is itself the recovery primitive: the Git
	// adapter replays only an exact content-free receipt and the store completes
	// the same operation. No completed operation row exists in the crash window.
	return facade.client.ApplyIntegrationCandidate(reconcileContext, operationID, input)
}

func validIntegrationMCPOutcome(result localapi.ApplyIntegrationCandidateResult) bool {
	if result.Strategy != application.IntegrationMerge && result.Strategy != application.IntegrationRebase &&
		result.Strategy != application.IntegrationCherryPick {
		return false
	}
	switch result.Outcome {
	case application.IntegrationApplied:
		return domain.ValidateGitRevision(result.ResultingHead) == nil &&
			result.ResultingHead != result.PreviousHead && len(result.ConflictPaths) == 0
	case application.IntegrationConflicted:
		return result.ResultingHead == "" && validIntegrationMCPConflictPaths(result.ConflictPaths)
	case application.IntegrationInvalidated:
		return result.ResultingHead == "" && len(result.ConflictPaths) == 0
	default:
		return false
	}
}

func validIntegrationMCPConflictPaths(paths []string) bool {
	if len(paths) == 0 || len(paths) > 256 || !sort.StringsAreSorted(paths) {
		return false
	}
	for index, path := range paths {
		if path == "" || len([]byte(path)) > 1024 || filepath.IsAbs(path) || filepath.Clean(path) != path ||
			path == "." || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) ||
			(index > 0 && paths[index-1] == path) {
			return false
		}
	}
	return true
}
