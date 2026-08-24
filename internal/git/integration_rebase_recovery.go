package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) recordIntegrationTargetRef(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
) error {
	existing, found, err := registry.recordedIntegrationTargetRef(ctx, request)
	if err != nil {
		return errors.New("apply integration candidate: target branch receipt is unavailable")
	}
	if found {
		if existing != targetRef {
			return errors.New("apply integration candidate: target branch receipt differs")
		}
		return nil
	}
	receipt := integrationReceiptRef("target", request)
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", receipt, targetRef); err != nil {
		return errors.New("apply integration candidate: target branch receipt could not be recorded")
	}
	return nil
}

func (registry *Registry) resumeRebaseIntegration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) (application.IntegrationAdapterResult, error) {
	if request.Strategy != application.IntegrationRebase {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: only a rebase conflict can be resumed")
	}
	previous := request
	previous.OperationID = request.RecoveryOperationID
	previous.RecoveryOperationID = ""
	conflictRef := integrationReceiptRef("conflicted", previous)
	conflictHead, found, err := registry.integrationReceiptHead(ctx, repository, conflictRef)
	if err != nil || !found || conflictHead != request.Target.ExpectedHead {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovery conflict receipt is unavailable")
	}
	targetRef, err := registry.integrationTargetRef(ctx, previous)
	if err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	rebasedRef := integrationReceiptRef("rebased", request)
	if resultingHead, found, err := registry.integrationReceiptHead(ctx, repository, rebasedRef); err != nil {
		return application.IntegrationAdapterResult{}, err
	} else if found {
		return registry.finalizeRecoveredRebase(ctx, request, repository, targetRef, resultingHead)
	}
	if resultingHead, completedErr := registry.completedRebaseContinuation(ctx, request, targetRef); completedErr == nil {
		return registry.finalizeRecoveredRebase(ctx, request, repository, targetRef, resultingHead)
	}
	conflicts, err := registry.validateRecoverableRebase(ctx, request, targetRef)
	if err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if len(conflicts) != 0 {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: rebase conflicts remain unresolved")
	}
	configuration := []string{
		"--no-optional-locks", "-C", request.Target.WorktreePath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false", "-c", "core.editor=true",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, append(configuration, "rebase", "--continue")...); err != nil {
		conflicts, conflictErr := registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
		if conflictErr == nil && len(conflicts) != 0 {
			return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: rebase continuation produced unresolved conflicts")
		}
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: rebase continuation failed without attributable conflicts")
	}
	resultingHead, err := registry.validRecoveredRebaseHead(ctx, request)
	if err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	return registry.finalizeRecoveredRebase(ctx, request, repository, targetRef, resultingHead)
}

func (registry *Registry) recordIntegrationRebaseProof(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	proofRef := integrationRebaseProofRef(request)
	proofHead, found, err := registry.integrationReceiptHeadAtPath(ctx, request.Target.WorktreePath, proofRef)
	if err != nil {
		return errors.New("apply integration candidate: rebase completion proof is unavailable")
	}
	if found {
		if proofHead != request.Candidate.HeadRevision {
			return errors.New("apply integration candidate: rebase completion proof differs")
		}
		return nil
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"update-ref", proofRef, request.Candidate.HeadRevision, integrationZeroRevision); err != nil {
		return errors.New("apply integration candidate: rebase completion proof could not be recorded")
	}
	return nil
}

func (registry *Registry) integrationTargetRef(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) (string, error) {
	targetRef, found, err := registry.recordedIntegrationTargetRef(ctx, request)
	if err != nil || !found {
		return "", errors.New("apply integration candidate: target branch receipt is invalid")
	}
	return targetRef, nil
}

func (registry *Registry) recordedIntegrationTargetRef(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) (string, bool, error) {
	receipt := integrationReceiptRef("target", request)
	inspected, err := registry.inspectIntegrationReceipt(ctx, request.Target.WorktreePath, receipt)
	if err != nil {
		return "", false, errors.New("apply integration candidate: target branch receipt is invalid")
	}
	switch inspected.kind {
	case integrationReceiptAbsent:
		return "", false, nil
	case integrationReceiptDirect:
		return "", false, errors.New("apply integration candidate: target branch receipt is ambiguous")
	case integrationReceiptSymbolic:
		if !strings.HasPrefix(inspected.value, "refs/heads/") {
			return "", false, errors.New("apply integration candidate: target branch receipt is invalid")
		}
		return inspected.value, true, nil
	default:
		return "", false, errors.New("apply integration candidate: target branch receipt is invalid")
	}
}

func (registry *Registry) reconcileInterruptedRebase(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
	conflictedRef string,
) (application.IntegrationAdapterResult, bool, error) {
	if request.Strategy != application.IntegrationRebase || request.RecoveryOperationID != "" {
		return application.IntegrationAdapterResult{}, false, nil
	}
	targetRef, found, err := registry.recordedIntegrationTargetRef(ctx, request)
	if err != nil || !found {
		return application.IntegrationAdapterResult{}, false, err
	}
	conflicts, err := registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if len(conflicts) != 0 {
		branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
		if err != nil || branchHead != request.Target.ExpectedHead {
			return application.IntegrationAdapterResult{}, true, errors.New("apply integration candidate: interrupted target branch differs")
		}
		result, _, err := registry.replayConflictedRebase(ctx, request, request.Target.ExpectedHead)
		if err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		if err := registry.createIntegrationReceipt(ctx, repository, conflictedRef, request.Target.ExpectedHead); err != nil {
			return application.IntegrationAdapterResult{}, true, errors.New("apply integration candidate: conflict receipt could not be recorded")
		}
		return result, true, nil
	}
	currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return application.IntegrationAdapterResult{}, true, errors.New("apply integration candidate: interrupted rebase head is unavailable")
	}
	headRef, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if currentHead == request.Target.ExpectedHead && attached && headRef == targetRef {
		return application.IntegrationAdapterResult{}, false, nil
	}
	if err := registry.validateRebaseOrigin(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	proofRef := integrationRebaseProofRef(request)
	resultingHead, err := registry.validRecoveredRebaseHead(ctx, request)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || (branchHead != request.Target.ExpectedHead && branchHead != resultingHead) ||
		(attached && headRef != proofRef && (headRef != targetRef || branchHead != resultingHead)) {
		return application.IntegrationAdapterResult{}, true, errors.New("apply integration candidate: interrupted rebase posture differs")
	}
	result, err := registry.finalizeRecoveredRebase(ctx, request, repository, targetRef, resultingHead)
	return result, true, err
}

func (registry *Registry) completedRebaseContinuation(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
) (string, error) {
	if err := registry.validateRebaseOrigin(ctx, request); err != nil {
		return "", err
	}
	headRef, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath)
	if err != nil || (attached && headRef != integrationRebaseProofRef(request)) {
		return "", errors.New("apply integration candidate: completed rebase continuation attachment differs")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || branchHead != request.Target.ExpectedHead {
		return "", errors.New("apply integration candidate: completed rebase continuation changed the target branch")
	}
	return registry.validRecoveredRebaseHead(ctx, request)
}

func (registry *Registry) validateRebaseOrigin(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) error {
	originalHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "ORIG_HEAD^{commit}")
	if err != nil || originalHead != request.Candidate.HeadRevision {
		return errors.New("apply integration candidate: rebase recovery origin differs")
	}
	return nil
}

func (registry *Registry) integrationBranchHead(ctx context.Context, worktreePath, targetRef string) (string, error) {
	return runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--verify", targetRef+"^{commit}")
}

func (registry *Registry) integrationHeadRef(ctx context.Context, worktreePath string) (string, bool, error) {
	attached, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"symbolic-ref", "--quiet", "HEAD")
	if err != nil || !attached {
		return "", false, err
	}
	headRef, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"symbolic-ref", "--quiet", "HEAD")
	if err != nil || !strings.HasPrefix(headRef, "refs/heads/") || strings.ContainsAny(headRef, "\x00\r\n\t ") {
		return "", false, errors.New("apply integration candidate: target attachment is invalid")
	}
	return headRef, true, nil
}

func (registry *Registry) validateRecoverableRebase(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
) ([]string, error) {
	branchHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", targetRef+"^{commit}")
	if err != nil || branchHead != request.Target.ExpectedHead {
		return nil, errors.New("apply integration candidate: target branch changed before recovery")
	}
	if err := registry.validateRebaseOrigin(ctx, request); err != nil {
		return nil, err
	}
	rebaseHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil {
		return nil, errors.New("apply integration candidate: rebase recovery state is unavailable")
	}
	baseContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", request.Candidate.BaseRevision, rebaseHead)
	if err != nil || !baseContains {
		return nil, errors.New("apply integration candidate: recovery conflict is outside candidate range")
	}
	candidateContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", rebaseHead, request.Candidate.HeadRevision)
	if err != nil || !candidateContains {
		return nil, errors.New("apply integration candidate: recovery conflict differs from candidate")
	}
	currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return nil, errors.New("apply integration candidate: recovery head is unavailable")
	}
	targetContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", request.Target.ExpectedHead, currentHead)
	if err != nil || !targetContains {
		return nil, errors.New("apply integration candidate: recovery head differs from target")
	}
	return registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
}

func (registry *Registry) validRecoveredRebaseHead(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) (string, error) {
	if err := registry.ensureRebaseSequencerAbsent(ctx, request.Target.WorktreePath); err != nil {
		return "", err
	}
	resultingHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !gitRevisionPattern.MatchString(resultingHead) || resultingHead == request.Target.ExpectedHead {
		return "", errors.New("apply integration candidate: recovered rebase head is invalid")
	}
	status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || len(status) != 0 {
		return "", errors.New("apply integration candidate: recovered rebase is not clean")
	}
	targetContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", request.Target.ExpectedHead, resultingHead)
	if err != nil || !targetContains {
		return "", errors.New("apply integration candidate: recovered rebase omits target history")
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return "", err
	}
	return resultingHead, nil
}

func (registry *Registry) promoteCompletedRebaseProof(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	rebasedRef := integrationReceiptRef("rebased", request)
	rebasedHead, rebasedFound, err := registry.integrationReceiptHeadAtPath(
		ctx, request.Target.WorktreePath, rebasedRef,
	)
	if err != nil {
		return errors.New("apply integration candidate: rebased head receipt is unavailable")
	}
	proofRef := integrationRebaseProofRef(request)
	proofHead, proofFound, err := registry.integrationReceiptHeadAtPath(
		ctx, request.Target.WorktreePath, proofRef,
	)
	if err != nil {
		return errors.New("apply integration candidate: rebase completion proof is unavailable")
	}
	if rebasedFound {
		if rebasedHead != resultingHead || (proofFound && proofHead != resultingHead) {
			return errors.New("apply integration candidate: rebase completion proof differs")
		}
	} else {
		if !proofFound || proofHead != resultingHead {
			return errors.New("apply integration candidate: rebase completion proof is unavailable")
		}
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"update-ref", rebasedRef, resultingHead, integrationZeroRevision); err != nil {
			return errors.New("apply integration candidate: rebased head receipt could not be recorded")
		}
	}
	return nil
}

func (registry *Registry) retireIntegrationRebaseProof(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	proofRef := integrationRebaseProofRef(request)
	proofHead, found, err := registry.integrationReceiptHeadAtPath(ctx, request.Target.WorktreePath, proofRef)
	if err != nil || (found && proofHead != resultingHead) {
		return errors.New("apply integration candidate: rebase completion proof differs")
	}
	if !found {
		return nil
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"update-ref", "-d", proofRef, resultingHead); err != nil {
		return errors.New("apply integration candidate: rebase completion proof could not be retired")
	}
	return nil
}

func (registry *Registry) ensureRebaseSequencerAbsent(ctx context.Context, worktreePath string) error {
	gitDir, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDir) {
		return errors.New("apply integration candidate: rebase sequencer is unavailable")
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		_, statErr := os.Lstat(filepath.Join(gitDir, name))
		if statErr == nil {
			return errors.New("apply integration candidate: rebase sequencer is still active")
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return errors.New("apply integration candidate: rebase sequencer is unavailable")
		}
	}
	return nil
}

func (registry *Registry) finalizeRecoveredRebase(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
	targetRef string,
	resultingHead string,
) (application.IntegrationAdapterResult, error) {
	currentHead, err := registry.validRecoveredRebaseHead(ctx, request)
	if err != nil || currentHead != resultingHead {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: rebased receipt differs from worktree")
	}
	branchHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", targetRef+"^{commit}")
	if err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target branch is unavailable")
	}
	if branchHead == request.Target.ExpectedHead {
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"update-ref", targetRef, resultingHead, request.Target.ExpectedHead); err != nil {
			return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target branch changed during recovery")
		}
	} else if branchHead != resultingHead {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target branch differs from recovered head")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "HEAD", targetRef); err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovered target could not be reattached")
	}
	if err := registry.retireIntegrationRebaseProof(ctx, request, resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	final, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || final.Cleanliness != CandidateClean || final.HeadRevision != resultingHead {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovered target is unverified")
	}
	if err := registry.createIntegrationReceipt(
		ctx, repository, integrationReceiptRef("applied", request), resultingHead,
	); err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: recovery applied receipt could not be recorded")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: resultingHead,
	}, nil
}
