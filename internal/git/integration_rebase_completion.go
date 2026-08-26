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
	commit         string
	indexDigest    string
	resolvedTree   string
	expectedResult string
	paths          []string
}

func (registry *Registry) runIntegrationStrategy(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) error {
	if request.Strategy == application.IntegrationRebase {
		return registry.runRebaseIntegration(ctx, request, repository)
	}
	plan, conflicted, err := registry.runIsolatedIntegration(ctx, request, repository)
	if err != nil {
		if errors.Is(err, errIntegrationSharedStateWritten) {
			return err
		}
		return errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	if !conflicted {
		if err := registry.validateIntegrationMaterializationResult(ctx, request, plan.ResultingHead); err != nil {
			return err
		}
		if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
			return errors.Join(err, application.ErrIntegrationMutationNotStarted)
		}
		if err := registry.validateIntegrationMutationDeadline(request); err != nil {
			return err
		}
		targetRef, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"symbolic-ref", "--quiet", "HEAD")
		if err != nil || targetRef != "refs/heads/"+expectedIntegrationTargetBranch(request) {
			return errors.Join(errors.New("apply integration candidate: target branch identity is unavailable"),
				application.ErrIntegrationMutationNotStarted)
		}
		return withoutIntegrationMutationNotStarted(
			registry.materializeIntegrationResult(ctx, request, targetRef, plan.ResultingHead),
		)
	}
	return errors.Join(errors.New("apply integration candidate: strategy conflicts in isolation"),
		application.ErrIntegrationMutationNotStarted)
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
		if errors.Is(err, errIntegrationSharedStateWritten) {
			return err
		}
		return errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	proof, found, err := registry.serverRebaseProof(repository, request)
	if err != nil {
		return err
	}
	if !found || proof.resultingHead == "" {
		return errors.New("apply integration candidate: rebase result proof is unavailable")
	}
	if err := registry.validateIntegrationMaterializationResult(ctx, request, proof.resultingHead); err != nil {
		return err
	}
	mutationAt := registry.clock().UTC()
	if mutationAt.IsZero() || !mutationAt.Before(request.EvidenceExpiresAt) {
		return errors.Join(
			errors.New("apply integration candidate: candidate evidence expired after isolated publication"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.recordIntegrationTargetRef(ctx, request, targetRef); err != nil {
		return err
	}
	return withoutIntegrationMutationNotStarted(
		registry.applyIsolatedRebaseResult(ctx, request, targetRef, proof.resultingHead),
	)
}

func (registry *Registry) prepareServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (returnErr error) {
	sharedStateWritten := false
	defer func() {
		if returnErr != nil && sharedStateWritten {
			returnErr = errors.Join(returnErr, errIntegrationSharedStateWritten)
		}
	}()
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
	isolated, err := registry.preflightRebaseSequence(ctx, repository, request, directory, commits, patches)
	sharedStateWritten = isolated.sharedStateWritten
	if err != nil {
		if sharedStateWritten {
			return err
		}
		return errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	if isolated.conflicted {
		return errors.New("apply integration candidate: rebase conflicts in isolation")
	}
	want := serverRebaseProof{
		operationID: request.OperationID, candidateCommits: commits, candidatePatches: patches,
	}
	want.resultingHead = isolated.head
	want.resultCommits = append([]string(nil), isolated.commits...)
	existing, found, err := readServerRebaseProof(path)
	if err != nil {
		return err
	}
	if found {
		if !sameServerRebaseProofIdentity(existing, want) {
			return errors.New("apply integration candidate: rebase range proof differs")
		}
		if want.resultingHead != "" && existing.resultingHead == "" {
			return replaceServerRebaseProof(directory, path, existing, want)
		}
		if want.resultingHead != "" && !sameServerRebaseProof(existing, want) {
			return errors.New("apply integration candidate: isolated rebase result proof differs")
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
	continued, resolved, boundConflicts, err := registry.currentServerRebasePrefix(ctx, repository, request, proof, rebaseHead)
	if err != nil {
		return err
	}
	want := proof
	want.continuedCommits = continued
	want.resolvedCommits = resolved
	want.conflicts = boundConflicts
	if existing, found := serverRebaseConflictForCommit(want.conflicts, rebaseHead); found {
		if existing.indexDigest != indexDigest || !sameRebaseCommits(existing.paths, conflicts) {
			return errors.New("apply integration candidate: conflicted server snapshot differs")
		}
	} else {
		want.conflicts = append(want.conflicts, serverRebaseConflict{
			commit: rebaseHead, indexDigest: indexDigest, paths: conflicts,
		})
	}
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
	resultCommits, err := registry.rebaseCommitRange(ctx, repository, request.Target.ExpectedHead, resultingHead)
	if err != nil {
		return errors.New("apply integration candidate: completed rebase range is unavailable")
	}
	resolved, boundConflicts, err := registry.advanceServerRebaseResults(ctx, repository, request, proof, resultCommits)
	if err != nil {
		return err
	}
	if !sameRebaseCommits(proof.continuedCommits, resultCommits) ||
		!sameRebaseCommits(proof.resolvedCommits, resolved) ||
		!sameServerRebaseConflicts(proof.conflicts, boundConflicts) {
		want := proof
		want.continuedCommits = append([]string(nil), resultCommits...)
		want.resolvedCommits = resolved
		want.conflicts = boundConflicts
		if err := replaceServerRebaseProof(directory, path, proof, want); err != nil {
			return err
		}
		proof = want
	}
	resultCommits, err = registry.verifyServerRebaseSemantics(ctx, repository, request, proof, resultingHead)
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
		!serverRebaseConflictsWereResolved(proof.resolvedCommits, proof.conflicts) ||
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
