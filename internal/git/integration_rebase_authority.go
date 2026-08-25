package git

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func originalIntegrationRequest(request application.IntegrationAdapterRequest) application.IntegrationAdapterRequest {
	if request.RecoveryOperationID == "" {
		return request
	}
	request.OperationID = request.RecoveryOperationID
	request.RecoveryOperationID = ""
	if request.OriginalEvidenceDigest != "" {
		request.Candidate.EvidenceDigest = request.OriginalEvidenceDigest
		request.EvidenceExpiresAt = request.OriginalEvidenceExpiresAt
	}
	request.OriginalEvidenceDigest = ""
	request.OriginalEvidenceExpiresAt = time.Time{}
	request.PendingMaterializationRecovery = false
	return request
}

func originalIntegrationOperationID(request application.IntegrationAdapterRequest) string {
	return originalIntegrationRequest(request).OperationID
}

func (registry *Registry) preflightRebaseSequence(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	directory string,
	commits []string,
	patches []string,
) (isolatedRebaseResult, error) {
	mergeCommits, err := runGitBytesWithLimit(ctx, maximumRebaseProofCommits*66, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "rev-list", "--min-parents=2",
		request.Candidate.BaseRevision+".."+request.Candidate.HeadRevision)
	if err != nil || len(mergeCommits) != 0 {
		return isolatedRebaseResult{}, errors.New("apply integration candidate: rebase range has unsupported merge topology")
	}
	seenPatches := make(map[string]struct{}, len(patches))
	for _, patch := range patches {
		if patch == "-" {
			return isolatedRebaseResult{}, errors.New("apply integration candidate: rebase range contains an empty commit")
		}
		if _, duplicate := seenPatches[patch]; duplicate {
			return isolatedRebaseResult{}, errors.New("apply integration candidate: rebase range contains duplicate content")
		}
		seenPatches[patch] = struct{}{}
	}
	cherry, err := runGitBytesWithLimit(ctx, maximumRebaseProofCommits*68, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "cherry",
		request.Target.ExpectedHead, request.Candidate.HeadRevision, request.Candidate.BaseRevision)
	if err != nil {
		return isolatedRebaseResult{}, errors.New("apply integration candidate: rebase uniqueness proof is unavailable")
	}
	unique := make(map[string]struct{}, len(commits))
	for _, line := range strings.Split(strings.TrimSuffix(string(cherry), "\n"), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "+" || !gitRevisionPattern.MatchString(fields[1]) {
			return isolatedRebaseResult{}, errors.New("apply integration candidate: target already represents candidate content")
		}
		unique[fields[1]] = struct{}{}
	}
	if len(unique) != len(commits) {
		return isolatedRebaseResult{}, errors.New("apply integration candidate: rebase uniqueness proof differs")
	}
	for _, commit := range commits {
		if _, found := unique[commit]; !found {
			return isolatedRebaseResult{}, errors.New("apply integration candidate: rebase uniqueness proof differs")
		}
	}
	result, err := registry.preflightRebasePatches(ctx, repository, request, directory, commits, patches)
	if err != nil {
		return isolatedRebaseResult{}, err
	}
	if result.conflicted {
		if _, err := registry.rebaseCommitContent(ctx, repository, commits[len(commits)-1]); err != nil {
			return isolatedRebaseResult{}, err
		}
	}
	return result, nil
}

func (registry *Registry) validateReceiptOnlyRebaseReceipts(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) (string, bool, error) {
	original := originalIntegrationRequest(request)
	for _, outcome := range []string{"conflicted", "applied"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, request),
		); err != nil {
			return "", false, errors.New("apply integration candidate: receipt-only completion receipt is unexpected")
		}
	}
	if request.RecoveryOperationID != "" {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, request.Target.WorktreePath, integrationReceiptRef("target", request),
		); err != nil {
			return "", false, errors.New("apply integration candidate: recovery target receipt is unexpected")
		}
		conflictedHead, found, err := registry.integrationReceiptHeadAtPath(
			ctx, request.Target.WorktreePath, integrationReceiptRef("conflicted", original),
		)
		if err != nil || !found || conflictedHead != request.Target.ExpectedHead {
			return "", false, errors.New("apply integration candidate: original conflict receipt differs")
		}
		for _, outcome := range []string{"applied", "rebased"} {
			if err := registry.requireIntegrationReceiptAbsent(
				ctx, request.Target.WorktreePath, integrationReceiptRef(outcome, original),
			); err != nil {
				return "", false, errors.New("apply integration candidate: original completion receipt is unexpected")
			}
		}
	}
	targetRef, found, err := registry.recordedIntegrationTargetRef(ctx, original)
	if err != nil || !found {
		return "", false, errors.New("apply integration candidate: receipt-only target receipt is unavailable")
	}
	rebasedHead, found, err := registry.integrationReceiptHeadAtPath(
		ctx, request.Target.WorktreePath, integrationReceiptRef("rebased", request),
	)
	if err != nil || found && rebasedHead != resultingHead {
		return "", false, errors.New("apply integration candidate: receipt-only rebased receipt differs")
	}
	proof, proofErr := registry.inspectIntegrationReceipt(
		ctx, request.Target.WorktreePath, integrationRebaseProofRef(request),
	)
	if proofErr != nil || proof.kind == integrationReceiptSymbolic ||
		proof.kind == integrationReceiptDirect && proof.value != resultingHead ||
		!found && proof.kind != integrationReceiptDirect {
		return "", false, errors.New("apply integration candidate: receipt-only proof receipt differs")
	}
	return targetRef, found, nil
}

func (registry *Registry) requireIntegrationReceiptAbsent(
	ctx context.Context,
	worktreePath string,
	reference string,
) error {
	receipt, err := registry.inspectIntegrationReceipt(ctx, worktreePath, reference)
	if err != nil || receipt.kind != integrationReceiptAbsent {
		return errors.New("apply integration candidate: receipt is not absent")
	}
	return nil
}
