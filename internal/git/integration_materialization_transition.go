package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type integrationMaterializationTransition struct {
	Version             int                             `json:"version"`
	State               string                          `json:"state"`
	OperationID         string                          `json:"operationId"`
	Strategy            application.IntegrationStrategy `json:"strategy"`
	TargetRef           string                          `json:"targetRef"`
	ExpectedHead        string                          `json:"expectedHead"`
	ExpectedTree        string                          `json:"expectedTree"`
	ExpectedIndexDigest string                          `json:"expectedIndexDigest"`
	CandidateBase       string                          `json:"candidateBase"`
	CandidateHead       string                          `json:"candidateHead"`
	ResultingHead       string                          `json:"resultingHead"`
	ResultingTree       string                          `json:"resultingTree"`
	RecoveryHead        string                          `json:"recoveryHead,omitempty"`
	RecoveryTree        string                          `json:"recoveryTree,omitempty"`
	RecoveryIndexDigest string                          `json:"recoveryIndexDigest,omitempty"`
}

func (registry *Registry) materializeIntegrationResult(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) error {
	repository, err := registry.Resolve(request.Target.RepositoryID)
	if err != nil {
		return errors.New("apply integration candidate: materialization repository is unavailable")
	}
	directory, path, err := integrationMaterializationPath(repository, request)
	if err != nil {
		return err
	}
	transition, found, err := readIntegrationMaterialization(path)
	if err != nil {
		return err
	}
	if found {
		if !integrationMaterializationMatches(transition, request, targetRef, resultingHead) {
			return errors.New("apply integration candidate: materialization transition differs")
		}
	} else {
		if request.Strategy == application.IntegrationRebase && request.RecoveryOperationID != "" &&
			!request.PreparedRestorationRecovery {
			transition, err = registry.prepareRecoveryIntegrationMaterialization(ctx, request, targetRef, resultingHead)
		} else {
			transition, err = registry.prepareIntegrationMaterialization(ctx, request, targetRef, resultingHead)
		}
		if err != nil {
			return errors.Join(err, application.ErrIntegrationMutationNotStarted)
		}
		if err := ensureServerRebaseProofDirectory(repository.WorktreeRoot, directory); err != nil {
			return err
		}
		if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
			return errors.Join(err, application.ErrIntegrationMutationNotStarted)
		}
		if err := registry.validateIntegrationMutationDeadline(request); err != nil {
			return err
		}
		if err := publishIntegrationMaterialization(directory, path, transition); err != nil {
			return err
		}
	}
	return registry.advanceIntegrationMaterialization(ctx, request, transition)
}

func (registry *Registry) reconcileIntegrationMaterialization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) (bool, error) {
	repository, err := registry.Resolve(request.Target.RepositoryID)
	if err != nil {
		return true, errors.New("apply integration candidate: materialization repository is unavailable")
	}
	_, path, err := integrationMaterializationPath(repository, request)
	if err != nil {
		return true, err
	}
	transition, found, err := readIntegrationMaterialization(path)
	if err != nil || !found {
		return found, err
	}
	if !integrationMaterializationMatches(transition, request, targetRef, resultingHead) {
		return true, errors.New("apply integration candidate: materialization transition differs")
	}
	if request.ReceiptOnly {
		completed, completedErr := registry.completedIntegrationMaterializationTransition(
			ctx, request, targetRef, resultingHead,
		)
		if completedErr == nil && completed {
			return true, nil
		}
		return true, errors.New("apply integration candidate: materialization mutation authority is unavailable")
	}
	return true, registry.advanceIntegrationMaterialization(ctx, request, transition)
}

func (registry *Registry) completedIntegrationMaterializationTransition(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) (bool, error) {
	repository, err := registry.Resolve(request.Target.RepositoryID)
	if err != nil {
		return false, errors.New("apply integration candidate: materialization repository is unavailable")
	}
	_, path, err := integrationMaterializationPath(repository, request)
	if err != nil {
		return false, err
	}
	transition, found, err := readIntegrationMaterialization(path)
	if err != nil || !found {
		return false, err
	}
	if !integrationMaterializationMatches(transition, request, targetRef, resultingHead) {
		return false, errors.New("apply integration candidate: materialization transition differs")
	}
	expectedTree, expectedErr := registry.integrationCommitTree(ctx, request.Target.WorktreePath, transition.ExpectedHead)
	resultingTree, resultingErr := registry.integrationCommitTree(ctx, request.Target.WorktreePath, transition.ResultingHead)
	if expectedErr != nil || resultingErr != nil || expectedTree != transition.ExpectedTree ||
		resultingTree != transition.ResultingTree {
		return false, errors.New("apply integration candidate: materialization tree proof differs")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, transition.TargetRef)
	if err != nil || branchHead != transition.ResultingHead || !registry.completedMaterialization(ctx, request, transition) {
		return false, errors.New("apply integration candidate: completed materialization is unverified")
	}
	return true, nil
}

func (registry *Registry) prepareIntegrationMaterialization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) (integrationMaterializationTransition, error) {
	if err := registry.validateIntegrationMaterializationResult(ctx, request, resultingHead); err != nil {
		return integrationMaterializationTransition{}, err
	}
	expectedTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, request.Target.ExpectedHead)
	if err != nil {
		return integrationMaterializationTransition{}, err
	}
	resultingTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, resultingHead)
	if err != nil {
		return integrationMaterializationTransition{}, err
	}
	indexDigest, err := registry.expectedMaterializationIdentity(ctx, request, targetRef)
	if err != nil {
		return integrationMaterializationTransition{}, err
	}
	return integrationMaterializationTransition{
		Version: 1, State: "pending", OperationID: request.OperationID, Strategy: request.Strategy,
		TargetRef: targetRef, ExpectedHead: request.Target.ExpectedHead, ExpectedTree: expectedTree,
		ExpectedIndexDigest: indexDigest, CandidateBase: request.Candidate.BaseRevision,
		CandidateHead: request.Candidate.HeadRevision, ResultingHead: resultingHead, ResultingTree: resultingTree,
	}, nil
}

func (registry *Registry) expectedMaterializationIdentity(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
) (string, error) {
	headRef, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath)
	if err != nil || !attached || headRef != targetRef {
		return "", errors.New("apply integration candidate: materialization attachment differs")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || branchHead != request.Target.ExpectedHead {
		return "", errors.New("apply integration candidate: materialization target differs")
	}
	expectedTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, request.Target.ExpectedHead)
	if err != nil {
		return "", err
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, expectedTree)
	if err != nil {
		return "", err
	}
	matches, err := integrationWorktreeMatchesMaterializationSnapshot(request.Target.WorktreePath, snapshot)
	if err != nil || !matches {
		return "", errors.New("apply integration candidate: materialization worktree is not clean")
	}
	indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil || indexTree != expectedTree {
		return "", errors.New("apply integration candidate: materialization index differs")
	}
	return registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
}

func (registry *Registry) advanceIntegrationMaterialization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	transition integrationMaterializationTransition,
) error {
	expectedTree, expectedErr := registry.integrationCommitTree(ctx, request.Target.WorktreePath, transition.ExpectedHead)
	resultingTree, resultingErr := registry.integrationCommitTree(ctx, request.Target.WorktreePath, transition.ResultingHead)
	if expectedErr != nil || resultingErr != nil || expectedTree != transition.ExpectedTree ||
		resultingTree != transition.ResultingTree {
		return errors.New("apply integration candidate: materialization tree proof differs")
	}
	var expectedSnapshot, resultingSnapshot integrationTreeSnapshot
	expectedSnapshot, snapshotErr := registry.loadIntegrationTreeSnapshot(
		ctx, request.Target.WorktreePath, transition.ExpectedTree,
	)
	if snapshotErr == nil {
		resultingSnapshot, snapshotErr = registry.loadIntegrationTreeSnapshot(
			ctx, request.Target.WorktreePath, transition.ResultingTree,
		)
		if snapshotErr == nil {
			snapshotErr = validateIntegrationMaterializationTopology(expectedSnapshot, resultingSnapshot)
		}
	}
	if snapshotErr != nil {
		branchHead, branchErr := registry.integrationBranchHead(
			ctx, request.Target.WorktreePath, transition.TargetRef,
		)
		if transition.State == "pending" && branchErr == nil && branchHead == transition.ExpectedHead {
			return errors.Join(snapshotErr, application.ErrIntegrationMutationNotStarted)
		}
		return snapshotErr
	}
	recoveryTransition := transition.State == "recovery"
	if recoveryTransition {
		restored, restoreErr := registry.restoreRecoveryMaterializationBase(ctx, request, transition)
		if restoreErr != nil {
			return withoutIntegrationMutationNotStarted(restoreErr)
		}
		transition = restored
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, transition.TargetRef)
	if err != nil {
		return errors.New("apply integration candidate: materialization target is unavailable")
	}
	if branchHead == transition.ExpectedHead {
		if err := registry.verifyExpectedMaterializationState(ctx, request, transition); err != nil {
			return err
		}
		if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
			if recoveryTransition {
				return withoutIntegrationMutationNotStarted(err)
			}
			return errors.Join(err, application.ErrIntegrationMutationNotStarted)
		}
		if err := registry.validateIntegrationMutationDeadline(request); err != nil {
			if recoveryTransition {
				return withoutIntegrationMutationNotStarted(err)
			}
			return err
		}
		if request.Strategy != application.IntegrationRebase && !request.PendingMaterializationRecovery {
			if err := registry.validateCompletedIntegrationReceiptFamily(ctx, request); err != nil {
				return err
			}
		}
		if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
			"update-ref", transition.TargetRef, transition.ResultingHead, transition.ExpectedHead); err != nil {
			current, currentErr := registry.integrationBranchHead(ctx, request.Target.WorktreePath, transition.TargetRef)
			if currentErr == nil && current == transition.ExpectedHead {
				return errors.Join(errors.New("apply integration candidate: result compare-and-swap did not start"),
					application.ErrIntegrationMutationNotStarted)
			}
			return errors.New("apply integration candidate: result compare-and-swap outcome requires reconciliation")
		}
		branchHead = transition.ResultingHead
		if request.Strategy != application.IntegrationRebase && !request.PendingMaterializationRecovery {
			if err := registry.validateCompletedIntegrationReceiptFamily(ctx, request); err != nil {
				return err
			}
		}
	}
	if branchHead != transition.ResultingHead {
		return errors.New("apply integration candidate: materialization target differs from proof")
	}
	if registry.completedMaterialization(ctx, request, transition) {
		return nil
	}
	matchesExpected, err := integrationWorktreeMatchesSnapshot(request.Target.WorktreePath, expectedSnapshot)
	recoveryAvailable, recoveryErr := integrationMaterializationRecoveryStatus(
		request.Target.WorktreePath, expectedSnapshot, resultingSnapshot,
	)
	if recoveryErr != nil || err != nil || !matchesExpected && !recoveryAvailable {
		return errors.New("apply integration candidate: post-CAS worktree identity differs")
	}
	indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil || indexTree != transition.ExpectedTree && indexTree != transition.ResultingTree {
		return errors.New("apply integration candidate: post-CAS index identity differs")
	}
	if indexTree == transition.ExpectedTree {
		digest, digestErr := registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
		if digestErr != nil || digest != transition.ExpectedIndexDigest {
			return errors.New("apply integration candidate: post-CAS index identity differs")
		}
	}
	workspace, err := registry.integrationMaterializationWorkspace(ctx, request.Target.WorktreePath)
	if err != nil {
		return err
	}
	if err := registry.authorizeIntegrationMaterializationAfterCAS(ctx, request, transition.ResultingHead); err != nil {
		return err
	}
	if indexTree == transition.ExpectedTree {
		if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
			"read-tree", "--reset", transition.ResultingHead); err != nil {
			return errors.New("apply integration candidate: proved result index could not be safely materialized")
		}
		indexTree, err = registry.integrationIndexTree(ctx, request.Target.WorktreePath)
		if err != nil || indexTree != transition.ResultingTree {
			return errors.New("apply integration candidate: proved result index is unverified")
		}
		if request.Strategy != application.IntegrationRebase && !request.PendingMaterializationRecovery {
			if err := registry.validateCompletedIntegrationReceiptFamily(ctx, request); err != nil {
				return err
			}
		}
	}
	if err := registry.authorizeIntegrationMaterializationAfterCAS(ctx, request, transition.ResultingHead); err != nil {
		return err
	}
	if err := materializeIntegrationWorktree(
		request.Target.WorktreePath, expectedSnapshot, resultingSnapshot,
	); err != nil {
		return err
	}
	if !registry.completedMaterialization(ctx, request, transition) {
		return errors.New("apply integration candidate: proved result materialization is unverified")
	}
	if request.Strategy != application.IntegrationRebase && !request.PendingMaterializationRecovery {
		if err := registry.validateCompletedIntegrationReceiptFamily(ctx, request); err != nil {
			return err
		}
	}
	return nil
}

func (registry *Registry) authorizeIntegrationMaterializationAfterCAS(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	var err error
	if request.PendingMaterializationRecovery {
		err = registry.authorizePendingMaterializationRecovery(ctx, request, resultingHead)
	} else if request.Strategy == application.IntegrationRebase {
		err = registry.authorizeRebaseFinalization(ctx, request, resultingHead)
	} else {
		// A fresh operation must still hold unexpired evidence to finish what it
		// started. Repository configuration is not re-read: the service's own
		// writer publishes exact bytes and never executes it, so a racing writer
		// must not strand an already-advanced target.
		if err = registry.validateCompletedIntegrationReceiptFamily(ctx, request); err == nil {
			if err = registry.validateIntegrationMutationDeadline(request); err == nil {
				err = registry.validateCompletedIntegrationReceiptFamily(ctx, request)
			}
		}
	}
	if err == nil {
		return nil
	}
	if errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		return errors.New("apply integration candidate: materialization authorization expired after target update")
	}
	return err
}

func (registry *Registry) verifyExpectedMaterializationState(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	transition integrationMaterializationTransition,
) error {
	digest, err := registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
	if err != nil || digest != transition.ExpectedIndexDigest {
		return errors.New("apply integration candidate: materialization index differs")
	}
	indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil || indexTree != transition.ExpectedTree {
		return errors.New("apply integration candidate: materialization index differs")
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, transition.ExpectedTree)
	if err != nil {
		return err
	}
	matches, err := integrationWorktreeMatchesMaterializationSnapshot(request.Target.WorktreePath, snapshot)
	if err != nil || !matches {
		return errors.New("apply integration candidate: materialization worktree differs")
	}
	return nil
}

func integrationMaterializationPath(
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, string, error) {
	reference := integrationReceiptRef("materialization", request)
	digest := strings.TrimPrefix(reference, "refs/comis/integration/materialization/")
	if len(digest) != 64 || !lowerHex(digest) {
		return "", "", errors.New("apply integration candidate: materialization identity is invalid")
	}
	directory := filepath.Join(repository.WorktreeRoot, ".comis-integration-proofs")
	return directory, filepath.Join(directory, "materialization-"+digest), nil
}

func integrationMaterializationMatches(
	transition integrationMaterializationTransition,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) bool {
	validState := transition.Version == 1 && transition.State == "pending" &&
		transition.RecoveryHead == "" && transition.RecoveryTree == "" && transition.RecoveryIndexDigest == "" ||
		transition.Version == 3 && transition.State == "recovery" && request.RecoveryOperationID != "" &&
			gitRevisionPattern.MatchString(transition.RecoveryHead) &&
			gitRevisionPattern.MatchString(transition.RecoveryTree) && len(transition.RecoveryIndexDigest) == 64 &&
			lowerHex(transition.RecoveryIndexDigest)
	return validState &&
		transition.OperationID == request.OperationID && transition.Strategy == request.Strategy &&
		transition.TargetRef == targetRef && transition.ExpectedHead == request.Target.ExpectedHead &&
		transition.CandidateBase == request.Candidate.BaseRevision && transition.CandidateHead == request.Candidate.HeadRevision &&
		transition.ResultingHead == resultingHead && gitRevisionPattern.MatchString(transition.ExpectedTree) &&
		gitRevisionPattern.MatchString(transition.ResultingTree) && len(transition.ExpectedIndexDigest) == 64 &&
		lowerHex(transition.ExpectedIndexDigest)
}

func withoutIntegrationMutationNotStarted(err error) error {
	if err == nil || !errors.Is(err, application.ErrIntegrationMutationNotStarted) {
		return err
	}
	return errors.New("apply integration candidate: mutation outcome requires reconciliation")
}

func publishIntegrationMaterialization(
	directory string,
	path string,
	transition integrationMaterializationTransition,
) error {
	contents, err := json.Marshal(transition)
	if err != nil {
		return errors.New("apply integration candidate: materialization transition cannot be encoded")
	}
	contents = append(contents, '\n')
	temporary := path + ".pending"
	if err := discardServerRebaseProofTemporary(temporary); err != nil {
		return err
	}
	if err := createServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: materialization transition could not be published")
	}
	return syncDirectory(directory)
}

func readIntegrationMaterialization(path string) (integrationMaterializationTransition, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return integrationMaterializationTransition{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 2 || info.Size() > 4096 {
		return integrationMaterializationTransition{}, false, errors.New("apply integration candidate: materialization transition is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return integrationMaterializationTransition{}, false, errors.New("apply integration candidate: materialization transition is unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > 4096 {
		return integrationMaterializationTransition{}, false, errors.New("apply integration candidate: materialization transition is unavailable")
	}
	var transition integrationMaterializationTransition
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&transition) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return integrationMaterializationTransition{}, false, errors.New("apply integration candidate: materialization transition is malformed")
	}
	return transition, true, nil
}
