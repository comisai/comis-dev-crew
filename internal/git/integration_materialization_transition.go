package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		if request.Strategy == application.IntegrationRebase && request.RecoveryOperationID != "" {
			transition, err = registry.prepareRecoveryIntegrationMaterialization(ctx, request, targetRef, resultingHead)
		} else {
			transition, err = registry.prepareIntegrationMaterialization(ctx, request, targetRef, resultingHead)
		}
		if err != nil {
			return err
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
	status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || len(status) != 0 || registry.materializationHasIgnoredFiles(ctx, request.Target.WorktreePath) {
		return "", errors.New("apply integration candidate: materialization worktree is not clean")
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
	}
	if branchHead != transition.ResultingHead {
		return errors.New("apply integration candidate: materialization target differs from proof")
	}
	if registry.completedMaterialization(ctx, request, transition) {
		return nil
	}
	if err := registry.verifyExpectedMaterializationState(ctx, request, transition); err != nil {
		return errors.New("apply integration candidate: post-CAS worktree identity differs")
	}
	workspace, err := registry.integrationMaterializationWorkspace(ctx, request.Target.WorktreePath)
	if err != nil {
		return err
	}
	if err := registry.authorizeIntegrationMaterializationAfterCAS(ctx, request, transition.ResultingHead); err != nil {
		return err
	}
	if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false",
		"read-tree", "-m", "-u", transition.ExpectedHead, transition.ResultingHead); err != nil {
		return errors.New("apply integration candidate: proved result could not be safely materialized")
	}
	if !registry.completedMaterialization(ctx, request, transition) {
		return errors.New("apply integration candidate: proved result materialization is unverified")
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
		if err = registry.validateIntegrationExecutionPolicy(ctx, request); err == nil {
			err = registry.validateIntegrationMutationDeadline(request)
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

func (registry *Registry) completedMaterialization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	transition integrationMaterializationTransition,
) bool {
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	return err == nil && target.HeadRevision == transition.ResultingHead && target.Cleanliness == CandidateClean &&
		target.Branch == expectedIntegrationTargetBranch(request)
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
	_, exitCode, err := executeGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"diff-files", "--quiet", "--ignore-submodules", "--")
	if err != nil || exitCode != 0 || registry.materializationHasUntrackedFiles(ctx, request.Target.WorktreePath) ||
		registry.materializationHasIgnoredFiles(ctx, request.Target.WorktreePath) {
		return errors.New("apply integration candidate: materialization worktree differs")
	}
	return nil
}

func (registry *Registry) materializationHasUntrackedFiles(ctx context.Context, worktreePath string) bool {
	output, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"ls-files", "--others", "--exclude-standard", "-z")
	return err != nil || len(output) != 0
}

func (registry *Registry) materializationHasIgnoredFiles(ctx context.Context, worktreePath string) bool {
	output, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	return err != nil || len(output) != 0
}

func (registry *Registry) integrationIndexDigest(ctx context.Context, worktreePath string) (string, error) {
	workspace, err := registry.integrationMaterializationWorkspace(ctx, worktreePath)
	if err != nil {
		return "", err
	}
	file, err := openRegularFile(workspace.gitIndex)
	if err != nil {
		return "", errors.New("apply integration candidate: target index is unavailable")
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, file)
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		return "", errors.New("apply integration candidate: target index could not be read")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func (registry *Registry) integrationMaterializationWorkspace(
	ctx context.Context,
	worktreePath string,
) (gitWorkspaceEnvironment, error) {
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDirectory) {
		return gitWorkspaceEnvironment{}, errors.New("apply integration candidate: target index identity is unavailable")
	}
	return gitWorkspaceEnvironment{
		gitDir: gitDirectory, gitWorkTree: worktreePath, gitIndex: filepath.Join(gitDirectory, "index"),
	}, nil
}

func (registry *Registry) integrationCommitTree(ctx context.Context, worktreePath, head string) (string, error) {
	tree, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--verify", head+"^{tree}")
	if err != nil || !gitRevisionPattern.MatchString(tree) {
		return "", errors.New("apply integration candidate: materialization tree is unavailable")
	}
	return tree, nil
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
		transition.RecoveryHead == "" && transition.RecoveryIndexDigest == "" ||
		transition.Version == 2 && transition.State == "recovery" && request.RecoveryOperationID != "" &&
			gitRevisionPattern.MatchString(transition.RecoveryHead) && len(transition.RecoveryIndexDigest) == 64 &&
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
