package git

import (
	"context"
	"errors"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const (
	maximumRebaseProofCommits = 4096
	maximumRebasePatchBytes   = 16 * 1024 * 1024
)

type serverRebaseProof struct {
	operationID      string
	candidateCommits []string
	candidatePatches []string
	resolvedCommits  []string
	conflicts        []serverRebaseConflict
	continuedCommits []string
	resultCommits    []string
	resultingHead    string
}

type serverRebaseConflict struct {
	commit      string
	indexDigest string
	paths       []string
}

func (registry *Registry) runIntegrationStrategy(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) error {
	if request.Strategy == application.IntegrationRebase {
		return registry.runRebaseIntegration(ctx, request, repository)
	}
	arguments := []string{
		"--no-optional-locks", "-C", request.Target.WorktreePath,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
	}
	switch request.Strategy {
	case application.IntegrationMerge:
		arguments = append(arguments, "merge", "--no-ff", "--no-edit", "--no-verify", "--no-stat", request.Candidate.HeadRevision)
	case application.IntegrationCherryPick:
		arguments = append(arguments, "cherry-pick", request.Candidate.BaseRevision+".."+request.Candidate.HeadRevision)
	default:
		return errors.New("apply integration candidate: strategy is invalid")
	}
	_, err := runGitBytes(ctx, registry.gitExecutable, arguments...)
	return err
}

func (registry *Registry) runRebaseIntegration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) error {
	targetRef, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "--quiet", "HEAD")
	if err != nil || targetRef != "refs/heads/"+expectedIntegrationTargetBranch(request) {
		return errors.Join(
			errors.New("apply integration candidate: target branch identity is unavailable"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	if err := registry.prepareServerRebaseProof(ctx, repository, request); err != nil {
		return errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	mutationAt := registry.clock().UTC()
	if mutationAt.IsZero() || !mutationAt.Before(request.EvidenceExpiresAt) {
		return errors.Join(
			errors.New("apply integration candidate: candidate evidence expired during rebase preflight"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	if err := registry.recordIntegrationTargetRef(ctx, request, targetRef); err != nil {
		return err
	}
	if err := registry.recordIntegrationRebaseProof(ctx, request); err != nil {
		return err
	}
	configuration := []string{
		"--no-optional-locks", "-C", request.Target.WorktreePath,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, append(configuration,
		"rebase", "--no-autostash", "--no-stat", "--reapply-cherry-picks", "--keep-empty",
		"--committer-date-is-author-date",
		"--onto", request.Target.ExpectedHead,
		request.Candidate.BaseRevision,
		strings.TrimPrefix(integrationRebaseProofRef(request), "refs/heads/"))...); err != nil {
		return err
	}
	resultingHead, err := registry.completeServiceRebase(ctx, repository, request)
	if err != nil {
		return err
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"update-ref", targetRef, resultingHead, request.Target.ExpectedHead); err != nil {
		return errors.New("apply integration candidate: target branch changed during rebase")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "HEAD", targetRef); err != nil {
		return errors.New("apply integration candidate: rebased target could not be reattached")
	}
	if err := registry.retireIntegrationRebaseProof(ctx, request, resultingHead); err != nil {
		return err
	}
	return nil
}

func (registry *Registry) prepareServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) error {
	directory, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	if err := ensureServerRebaseProofDirectory(repository.WorktreeRoot, directory); err != nil {
		return err
	}
	commits, err := registry.rebaseCommitRange(
		ctx, repository, request.Candidate.BaseRevision, request.Candidate.HeadRevision,
	)
	if err != nil {
		return errors.Join(
			errors.New("apply integration candidate: rebase range proof is unavailable"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	patches := make([]string, len(commits))
	for index, commit := range commits {
		patches[index], err = registry.rebasePatchIdentity(ctx, repository, commit)
		if err != nil {
			return errors.Join(
				errors.New("apply integration candidate: candidate patch proof is unavailable"),
				application.ErrIntegrationMutationNotStarted,
			)
		}
	}
	if err := registry.preflightRebaseSequence(ctx, repository, request, directory, commits, patches); err != nil {
		return errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	want := serverRebaseProof{
		operationID: request.OperationID, candidateCommits: commits, candidatePatches: patches,
	}
	existing, found, err := readServerRebaseProof(path)
	if err != nil {
		return err
	}
	if found {
		if !sameServerRebaseProofIdentity(existing, want) {
			return errors.New("apply integration candidate: rebase range proof differs")
		}
		if err := discardServerRebaseProofTemporary(path + ".pending"); err != nil {
			return err
		}
		return syncDirectory(directory)
	}
	return publishInitialServerRebaseProof(directory, path, want)
}

func (registry *Registry) recordServerRebaseConflict(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) error {
	rebaseHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil {
		return errors.New("apply integration candidate: conflicted rebase identity is unavailable")
	}
	conflicts, err := registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
	if err != nil || len(conflicts) == 0 {
		return errors.New("apply integration candidate: conflicted rebase paths are unavailable")
	}
	directory, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil || !found || proof.operationID != originalIntegrationOperationID(request) || proof.resultingHead != "" {
		return errors.New("apply integration candidate: conflicted server rebase proof is unavailable")
	}
	candidates, err := registry.rebaseCommitRange(
		ctx, repository, request.Candidate.BaseRevision, request.Candidate.HeadRevision,
	)
	if err != nil || !sameRebaseCommits(proof.candidateCommits, candidates) || !containsRebaseCommit(candidates, rebaseHead) {
		return errors.New("apply integration candidate: conflicted server rebase proof differs")
	}
	if err := registry.validateReconstructedRebaseConflict(
		ctx, repository, request, directory, rebaseHead, conflicts,
	); err != nil {
		return err
	}
	indexDigest, err := registry.rebaseProtectedIndexDigest(ctx, request.Target.WorktreePath, conflicts)
	if err != nil {
		return err
	}
	continued, err := registry.currentServerRebasePrefix(ctx, repository, request, proof, rebaseHead)
	if err != nil {
		return err
	}
	want := proof
	want.continuedCommits = continued
	want.resolvedCommits = appendResolvedRebaseCommit(candidates, proof.resolvedCommits, rebaseHead)
	want.conflicts = []serverRebaseConflict{{
		commit: rebaseHead, indexDigest: indexDigest, paths: conflicts,
	}}
	if sameRebaseCommits(want.resolvedCommits, proof.resolvedCommits) &&
		sameRebaseCommits(want.continuedCommits, proof.continuedCommits) &&
		sameServerRebaseConflicts(want.conflicts, proof.conflicts) {
		if err := discardServerRebaseProofTemporary(path + ".next"); err != nil {
			return err
		}
		return syncDirectory(directory)
	}
	return replaceServerRebaseProof(directory, path, proof, want)
}

func (registry *Registry) completeServiceRebase(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, error) {
	resultingHead, err := registry.inspectRecoveredRebaseHead(ctx, request)
	if err != nil {
		return "", err
	}
	if err := registry.completeServerRebaseProof(ctx, repository, request, resultingHead); err != nil {
		return "", err
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return "", err
	}
	return resultingHead, nil
}

func (registry *Registry) validRecoveredRebaseHead(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, error) {
	resultingHead, err := registry.inspectRecoveredRebaseHead(ctx, request)
	if err != nil {
		return "", err
	}
	if err := registry.requireServerRebaseProof(ctx, repository, request, resultingHead); err != nil {
		return "", err
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return "", err
	}
	return resultingHead, nil
}

func (registry *Registry) completeServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	directory, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil || !found || proof.operationID != originalIntegrationOperationID(request) ||
		proof.resultingHead != "" && proof.resultingHead != resultingHead {
		return errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	resultCommits, err := registry.verifyServerRebaseSemantics(ctx, repository, request, proof, resultingHead)
	if err != nil {
		return err
	}
	if proof.resultingHead == resultingHead {
		if !sameRebaseCommits(proof.resultCommits, resultCommits) {
			return errors.New("apply integration candidate: completed server rebase proof differs")
		}
		if err := discardServerRebaseProofTemporary(path + ".next"); err != nil {
			return err
		}
		return syncDirectory(directory)
	}
	want := proof
	want.resultCommits = resultCommits
	want.resultingHead = resultingHead
	return replaceServerRebaseProof(directory, path, proof, want)
}

func (registry *Registry) requireServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	_, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil || !found || proof.operationID != originalIntegrationOperationID(request) ||
		proof.resultingHead != resultingHead || len(proof.resultCommits) == 0 {
		return errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	resultCommits, err := registry.verifyServerRebaseSemantics(ctx, repository, request, proof, resultingHead)
	if err != nil || !sameRebaseCommits(proof.resultCommits, resultCommits) {
		return errors.New("apply integration candidate: server rebase range proof differs")
	}
	return nil
}

func (registry *Registry) reconcileReceiptOnlyCompletedRebase(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, bool, error) {
	if request.Strategy != application.IntegrationRebase {
		return application.IntegrationAdapterResult{}, false, nil
	}
	_, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if !found || proof.resultingHead == "" || len(proof.resultCommits) == 0 {
		return application.IntegrationAdapterResult{}, false, nil
	}
	if proof.operationID != originalIntegrationOperationID(request) {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only proof identity differs")
	}
	if err := registry.requireServerRebaseProof(ctx, repository, request, proof.resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if err := registry.ensureRebaseSequencerAbsent(ctx, request.Target.WorktreePath); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	targetRef, err := registry.validateReceiptOnlyRebaseReceipts(ctx, request, proof.resultingHead)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	proofRef := integrationRebaseProofRef(request)
	proofHead, proofFound, err := registry.integrationReceiptHeadAtPath(
		ctx, request.Target.WorktreePath, proofRef,
	)
	if err != nil || proofFound && proofHead != proof.resultingHead {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only proof receipt differs")
	}
	targetHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || targetHead != proof.resultingHead {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only target branch differs")
	}
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || target.Cleanliness != CandidateClean || target.HeadRevision != proof.resultingHead {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: receipt-only completed rebase differs")
	}
	expectedBranch := expectedIntegrationTargetBranch(request)
	if target.Branch != expectedBranch {
		if !proofFound || target.Branch != strings.TrimPrefix(proofRef, "refs/heads/") {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only completed rebase differs")
		}
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"symbolic-ref", "HEAD", targetRef); err != nil {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only target could not be reattached")
		}
		target, err = registry.InspectCandidate(ctx, CandidateSnapshotRequest{
			TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
			WorktreePath: request.Target.WorktreePath,
		})
		if err != nil || target.Cleanliness != CandidateClean || target.HeadRevision != proof.resultingHead ||
			target.Branch != expectedBranch {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: receipt-only reattached target differs")
		}
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: proof.resultingHead,
	}, true, nil
}

func (registry *Registry) verifyServerRebaseSemantics(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	proof serverRebaseProof,
	resultingHead string,
) ([]string, error) {
	candidates, candidateErr := registry.rebaseCommitRange(
		ctx, repository, request.Candidate.BaseRevision, request.Candidate.HeadRevision,
	)
	results, resultErr := registry.rebaseCommitRange(ctx, repository, request.Target.ExpectedHead, resultingHead)
	if candidateErr != nil || resultErr != nil || !sameRebaseCommits(proof.candidateCommits, candidates) ||
		len(candidates) != len(results) || !validResolvedRebaseCommits(candidates, proof.resolvedCommits) ||
		len(results) == 0 || results[len(results)-1] != resultingHead ||
		len(proof.continuedCommits) > len(results) ||
		!sameRebaseCommits(proof.continuedCommits, results[:len(proof.continuedCommits)]) {
		return nil, errors.New("apply integration candidate: server rebase range proof differs")
	}
	resolved := make(map[string]struct{}, len(proof.resolvedCommits))
	for _, commit := range proof.resolvedCommits {
		resolved[commit] = struct{}{}
	}
	for index, candidate := range candidates {
		if _, allowed := resolved[candidate]; allowed {
			continue
		}
		resultPatch, resultErr := registry.rebasePatchIdentity(ctx, repository, results[index])
		if resultErr != nil || proof.candidatePatches[index] != resultPatch {
			return nil, errors.New("apply integration candidate: rebased result differs from candidate content")
		}
	}
	return results, nil
}

func (registry *Registry) rebasePatchIdentity(
	ctx context.Context,
	repository Repository,
	revision string,
) (string, error) {
	patch, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "show", "--format=%H", "--no-color",
		"--no-ext-diff", "--no-textconv", "--no-renames", "--full-index", "--binary", revision)
	if err != nil {
		return "", err
	}
	output, err := runGitBytesWithInputAndLimit(ctx, patch, 256, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "patch-id", "--verbatim")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "-", nil
	}
	if len(fields) != 2 || !gitRevisionPattern.MatchString(fields[0]) || fields[1] != revision {
		return "", errors.New("rebase patch identity is invalid")
	}
	return fields[0], nil
}

func (registry *Registry) rebaseCommitRange(
	ctx context.Context,
	repository Repository,
	base string,
	head string,
) ([]string, error) {
	output, err := runGitBytesWithLimit(ctx, maximumRebaseProofCommits*66, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "rev-list", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, errors.New("rebase range is empty")
	}
	if len(lines) > maximumRebaseProofCommits {
		return nil, errors.New("rebase range exceeds its bound")
	}
	for _, line := range lines {
		if !gitRevisionPattern.MatchString(line) {
			return nil, errors.New("rebase range contains an invalid revision")
		}
	}
	return lines, nil
}
