package git

import (
	"context"
	"errors"
	"os"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) resumePreparedRebaseRestoration(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, bool, error) {
	if request.Strategy != application.IntegrationRebase || request.RecoveryOperationID == "" ||
		!request.PendingMaterializationRecovery {
		return application.IntegrationAdapterResult{}, false, nil
	}
	original := originalIntegrationRequest(request)
	directory, path, err := preparedRebaseRestorationPath(repository, original)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	restoration, found, err := readPreparedRebaseRestoration(path)
	if err != nil || !found {
		return application.IntegrationAdapterResult{}, found, err
	}
	targetRef := "refs/heads/" + expectedIntegrationTargetBranch(original)
	proofRef := integrationRebaseProofRef(original)
	if !preparedRebaseRestorationMatches(restoration, original, targetRef, proofRef) ||
		restoration.AdoptedBy != "" && restoration.AdoptedBy != request.OperationID {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: prepared restoration recovery differs")
	}
	if err := registry.validatePreparedRestorationPosture(ctx, original, restoration); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if err := registry.authorizePreparedRestoration(ctx, request, original, restoration); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if restoration.AdoptedBy == "" {
		adopted := restoration
		adopted.AdoptedBy = request.OperationID
		if err := replacePreparedRebaseRestoration(directory, path, restoration, adopted); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		restoration = adopted
	}
	request.PreparedRestorationRecovery = true
	if err := registry.advancePreparedRebaseRestoration(ctx, request, original, restoration); err != nil {
		return application.IntegrationAdapterResult{}, true, withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.prepareServerRebaseProof(ctx, repository, original); err != nil {
		return application.IntegrationAdapterResult{}, true, withoutIntegrationMutationNotStarted(err)
	}
	proof, found, err := registry.serverRebaseProof(repository, original)
	if err != nil || !found || proof.resultingHead == "" {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: prepared restoration result proof is unavailable")
	}
	if err := registry.applyIsolatedRebaseResult(ctx, request, targetRef, proof.resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true, withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.createIntegrationReceipt(
		ctx, repository, integrationReceiptRef("applied", request), proof.resultingHead,
	); err != nil {
		return application.IntegrationAdapterResult{}, true,
			errors.New("apply integration candidate: prepared recovery receipt could not be recorded")
	}
	if err := registry.validateAppliedIntegrationReceiptFamily(ctx, request, proof.resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, true, withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return application.IntegrationAdapterResult{}, true, withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.validateAppliedIntegrationReceiptFamily(ctx, request, proof.resultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	if err := retirePreparedRebaseRestoration(directory, path); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: proof.resultingHead,
	}, true, nil
}

func (registry *Registry) validatePreparedRestorationReceiptFamily(
	ctx context.Context,
	authority application.IntegrationAdapterRequest,
	identity application.IntegrationAdapterRequest,
	restoration preparedRebaseRestoration,
) error {
	worktree := identity.Target.WorktreePath
	if err := registry.requireSymbolicIntegrationReceipt(
		ctx, worktree, integrationReceiptRef("target", identity), restoration.TargetRef,
	); err != nil {
		return errors.New("apply integration candidate: prepared target receipt differs")
	}
	if err := registry.requireDirectIntegrationReceipt(
		ctx, worktree, restoration.ProofRef, restoration.CandidateHead,
	); err != nil {
		return errors.New("apply integration candidate: prepared proof receipt differs")
	}
	for _, outcome := range []string{"conflicted", "applied", "rebased"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef(outcome, identity),
		); err != nil {
			return errors.New("apply integration candidate: prepared receipt family is contradictory")
		}
	}
	if authority.OperationID == identity.OperationID {
		return nil
	}
	if restoration.AdoptedBy != "" && restoration.AdoptedBy != authority.OperationID {
		return errors.New("apply integration candidate: prepared restoration adoption differs")
	}
	for _, outcome := range []string{"target", "conflicted", "applied", "rebased"} {
		if err := registry.requireIntegrationReceiptAbsent(
			ctx, worktree, integrationReceiptRef(outcome, authority),
		); err != nil {
			return errors.New("apply integration candidate: prepared recovery receipt family is contradictory")
		}
	}
	return nil
}

func (registry *Registry) validatePreparedRestorationPosture(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	restoration preparedRebaseRestoration,
) error {
	candidateTree, candidateTreeErr := registry.integrationCommitTree(
		ctx, request.Target.WorktreePath, restoration.CandidateHead,
	)
	expectedTree, expectedTreeErr := registry.integrationCommitTree(
		ctx, request.Target.WorktreePath, restoration.ExpectedHead,
	)
	branchHead, branchErr := registry.integrationBranchHead(ctx, request.Target.WorktreePath, restoration.TargetRef)
	if candidateTreeErr != nil || expectedTreeErr != nil || branchErr != nil ||
		candidateTree != restoration.CandidateTree || expectedTree != restoration.ExpectedTree ||
		branchHead != restoration.ExpectedHead {
		return errors.New("apply integration candidate: prepared restoration proof differs")
	}
	candidate, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, restoration.CandidateTree)
	if err != nil {
		return err
	}
	expected, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, restoration.ExpectedTree)
	if err != nil {
		return err
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return errors.New("apply integration candidate: prepared restoration head is unavailable")
	}
	headRef, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath)
	if err != nil || !attached {
		return errors.New("apply integration candidate: prepared restoration attachment differs")
	}
	indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil {
		return err
	}
	candidateWorktree, candidateErr := integrationWorktreeMatchesSnapshot(request.Target.WorktreePath, candidate)
	expectedWorktree, expectedErr := integrationWorktreeMatchesSnapshot(request.Target.WorktreePath, expected)
	if candidateErr != nil || expectedErr != nil {
		return errors.New("apply integration candidate: prepared restoration worktree is unavailable")
	}
	if headRef == restoration.TargetRef && head == restoration.ExpectedHead &&
		indexTree == restoration.ExpectedTree && expectedWorktree {
		return nil
	}
	if headRef != restoration.ProofRef || head != restoration.CandidateHead {
		return errors.New("apply integration candidate: prepared restoration identity differs")
	}
	if indexTree == restoration.CandidateTree && candidateWorktree {
		digest, digestErr := registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
		if digestErr == nil && digest == restoration.CandidateIndex {
			return nil
		}
	}
	if indexTree == restoration.ExpectedTree {
		recoveryAvailable, recoveryErr := integrationMaterializationRecoveryStatus(
			request.Target.WorktreePath, candidate, expected,
		)
		if recoveryErr == nil && (candidateWorktree || expectedWorktree || recoveryAvailable) {
			return nil
		}
	}
	return errors.New("apply integration candidate: prepared restoration state is contradictory")
}

func replacePreparedRebaseRestoration(
	directory string,
	path string,
	previous preparedRebaseRestoration,
	next preparedRebaseRestoration,
) error {
	current, found, err := readPreparedRebaseRestoration(path)
	if err != nil || !found || current != previous {
		return errors.New("apply integration candidate: prepared restoration changed before adoption")
	}
	contents, err := encodePreparedRebaseRestoration(next)
	if err != nil {
		return err
	}
	temporary := path + ".next"
	if err := discardServerRebaseProofTemporary(temporary); err != nil {
		return err
	}
	if err := createServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: prepared restoration adoption could not be published")
	}
	return syncDirectory(directory)
}
