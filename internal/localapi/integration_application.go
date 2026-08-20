package localapi

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ApplyIntegrationCandidateInput names exact durable identities and Git heads.
// Host paths, policy selection, and Git strategy remain service-owned.
type ApplyIntegrationCandidateInput struct {
	InitiativeHandle        string `json:"initiativeHandle"`
	IntegrationTaskHandle   string `json:"integrationTaskHandle"`
	CandidateTaskHandle     string `json:"candidateTaskHandle"`
	CandidateHead           string `json:"candidateHead"`
	ExpectedIntegrationHead string `json:"expectedIntegrationHead"`
}

type applyIntegrationCandidateContract struct {
	IntegrationTaskHandle   string `json:"integrationTaskHandle"`
	CandidateTaskHandle     string `json:"candidateTaskHandle"`
	CandidateHead           string `json:"candidateHead"`
	ExpectedIntegrationHead string `json:"expectedIntegrationHead"`
}

// DecodeApplyIntegrationCandidateInput reads one strict bounded operator
// contract whose initiative is supplied separately by the visible command.
func DecodeApplyIntegrationCandidateInput(data []byte) (ApplyIntegrationCandidateInput, error) {
	if len(data) == 0 || len(data) > MaxRequestBytes {
		return ApplyIntegrationCandidateInput{}, errors.New("integration application input exceeds its bound")
	}
	var contract applyIntegrationCandidateContract
	if err := decodeObject(data, &contract); err != nil {
		return ApplyIntegrationCandidateInput{}, err
	}
	return ApplyIntegrationCandidateInput{
		IntegrationTaskHandle: contract.IntegrationTaskHandle,
		CandidateTaskHandle:   contract.CandidateTaskHandle, CandidateHead: contract.CandidateHead,
		ExpectedIntegrationHead: contract.ExpectedIntegrationHead,
	}, nil
}

// ApplyIntegrationCandidateResult is the path-free local boundary projection.
type ApplyIntegrationCandidateResult struct {
	SchemaVersion         int                             `json:"schemaVersion"`
	OperationID           string                          `json:"operationId"`
	InitiativeHandle      string                          `json:"initiativeHandle"`
	IntegrationTaskHandle string                          `json:"integrationTaskHandle"`
	CandidateTaskHandle   string                          `json:"candidateTaskHandle"`
	RepositoryID          string                          `json:"repositoryId"`
	CandidateHead         string                          `json:"candidateHead"`
	EvidenceDigest        string                          `json:"evidenceDigest"`
	Strategy              application.IntegrationStrategy `json:"strategy"`
	Outcome               application.IntegrationOutcome  `json:"outcome"`
	PreviousHead          string                          `json:"previousHead"`
	ResultingHead         string                          `json:"resultingHead,omitempty"`
	ConflictPaths         []string                        `json:"conflictPaths,omitempty"`
	StateVersion          int64                           `json:"stateVersion"`
	CompletedAtMs         int64                           `json:"completedAtMs"`
	SideEffect            SideEffectClass                 `json:"sideEffect"`
}

// ApplyIntegrationCandidate invokes the canonical integration coordinator.
func (client *Client) ApplyIntegrationCandidate(
	ctx context.Context,
	operationID string,
	input ApplyIntegrationCandidateInput,
) (ApplyIntegrationCandidateResult, error) {
	var result ApplyIntegrationCandidateResult
	err := client.call(ctx, operationID, MethodApplyIntegration, input, &result)
	return result, err
}

func (handler *Handler) dispatchIntegrationApplication(ctx context.Context, request Request) (Outcome, bool) {
	if request.Method != MethodApplyIntegration {
		return Outcome{}, false
	}
	var input ApplyIntegrationCandidateInput
	if err := decodeObject(request.Payload, &input); err != nil {
		return invalidPayload(request.OperationID, err), true
	}
	if handler.integrations == nil {
		return rejectedOutcome(
			request.OperationID, domain.ErrorUnavailable, true,
			"integration application service is unavailable", "inspect service configuration", nil,
		), true
	}
	result, err := handler.integrations.ApplyCandidate(ctx, application.ApplyIntegrationCandidateCommand{
		OperationID: request.OperationID, InitiativeHandle: input.InitiativeHandle,
		IntegrationTaskHandle: input.IntegrationTaskHandle, CandidateTaskHandle: input.CandidateTaskHandle,
		CandidateHead: input.CandidateHead, ExpectedIntegrationHead: input.ExpectedIntegrationHead,
	})
	if err != nil {
		return outcomeFromError(request.OperationID, err), true
	}
	if !validIntegrationApplicationResult(result, request.OperationID, input) {
		return rejectedOutcome(
			request.OperationID, domain.ErrorInternal, false,
			"integration application outcome is incomplete", "inspect durable service state", nil,
		), true
	}
	projection := ApplyIntegrationCandidateResult{
		SchemaVersion: 1, OperationID: result.OperationID,
		InitiativeHandle: result.InitiativeHandle, IntegrationTaskHandle: result.IntegrationTaskHandle,
		CandidateTaskHandle: result.Candidate.TaskHandle, RepositoryID: result.Candidate.RepositoryID,
		CandidateHead: result.Candidate.HeadRevision, EvidenceDigest: result.Candidate.EvidenceDigest,
		Strategy: result.Strategy, Outcome: result.Outcome, PreviousHead: result.PreviousHead,
		ResultingHead: result.ResultingHead, ConflictPaths: append([]string(nil), result.ConflictPaths...),
		StateVersion: result.StateVersion, CompletedAtMs: result.CompletedAt.UnixMilli(),
		SideEffect: MethodApplyIntegration.SideEffect(),
	}
	return queryOutcome(request.OperationID, projection.StateVersion, projection, nil), true
}

func validIntegrationApplicationResult(
	result application.IntegrationApplicationResult,
	operationID string,
	input ApplyIntegrationCandidateInput,
) bool {
	if result.OperationID != operationID || result.InitiativeHandle != input.InitiativeHandle ||
		result.IntegrationTaskHandle != input.IntegrationTaskHandle || result.Candidate.TaskHandle != input.CandidateTaskHandle ||
		result.Candidate.HeadRevision != input.CandidateHead || result.PreviousHead != input.ExpectedIntegrationHead ||
		domain.ValidateRepositoryID(result.Candidate.RepositoryID) != nil ||
		domain.ValidateGitRevision(result.Candidate.BaseRevision) != nil ||
		domain.ValidateBriefRevisionHash(result.Candidate.EvidenceDigest) != nil ||
		result.StateVersion < 1 || result.CompletedAt.IsZero() || result.CompletedAt.Location() != time.UTC {
		return false
	}
	if result.Strategy != application.IntegrationMerge && result.Strategy != application.IntegrationRebase &&
		result.Strategy != application.IntegrationCherryPick {
		return false
	}
	switch result.Outcome {
	case application.IntegrationApplied:
		return domain.ValidateGitRevision(result.ResultingHead) == nil && result.ResultingHead != result.PreviousHead && len(result.ConflictPaths) == 0
	case application.IntegrationConflicted:
		return result.ResultingHead == "" && validIntegrationConflictPaths(result.ConflictPaths)
	default:
		return false
	}
}

func validIntegrationConflictPaths(paths []string) bool {
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
