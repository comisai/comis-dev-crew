package git

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) prepareRecoveryIntegrationMaterialization(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	resultingHead string,
) (integrationMaterializationTransition, error) {
	if request.RecoveryOperationID == "" || targetRef != "refs/heads/"+expectedIntegrationTargetBranch(request) {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery materialization identity is invalid")
	}
	if err := registry.validateIntegrationMaterializationResult(ctx, request, resultingHead); err != nil {
		return integrationMaterializationTransition{}, err
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || branchHead != request.Target.ExpectedHead {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery target differs")
	}
	recoveryHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !gitRevisionPattern.MatchString(recoveryHead) {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery head is unavailable")
	}
	if _, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath); err != nil || attached {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery attachment differs")
	}
	indexDigest, err := registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
	if err != nil || !registry.recoveryMaterializationWorktreeMatchesIndex(ctx, request.Target.WorktreePath) {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery worktree identity differs")
	}
	recoveryTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
	if err != nil {
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
	return integrationMaterializationTransition{
		Version: 3, State: "recovery", OperationID: request.OperationID, Strategy: request.Strategy,
		TargetRef: targetRef, ExpectedHead: request.Target.ExpectedHead, ExpectedTree: expectedTree,
		ExpectedIndexDigest: indexDigest, CandidateBase: request.Candidate.BaseRevision,
		CandidateHead: request.Candidate.HeadRevision, ResultingHead: resultingHead, ResultingTree: resultingTree,
		RecoveryHead: recoveryHead, RecoveryTree: recoveryTree, RecoveryIndexDigest: indexDigest,
	}, nil
}

func (registry *Registry) restoreRecoveryMaterializationBase(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	transition integrationMaterializationTransition,
) (integrationMaterializationTransition, error) {
	if transition.Version != 3 || transition.State != "recovery" || request.RecoveryOperationID == "" {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery transition is invalid")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, transition.TargetRef)
	if err != nil || branchHead != transition.ExpectedHead {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery target changed")
	}
	recoverySnapshot, err := registry.loadIntegrationTreeSnapshot(
		ctx, request.Target.WorktreePath, transition.RecoveryTree,
	)
	if err != nil {
		return integrationMaterializationTransition{}, err
	}
	expectedSnapshot, err := registry.loadIntegrationTreeSnapshot(
		ctx, request.Target.WorktreePath, transition.ExpectedTree,
	)
	if err != nil {
		return integrationMaterializationTransition{}, err
	}
	for {
		currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
			request.Target.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery head is unavailable")
		}
		headRef, attached, err := registry.integrationHeadRef(ctx, request.Target.WorktreePath)
		if err != nil || attached && headRef != transition.TargetRef ||
			!attached && currentHead != transition.RecoveryHead ||
			attached && currentHead != transition.ExpectedHead {
			return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery attachment changed")
		}
		indexTree, err := registry.integrationIndexTree(ctx, request.Target.WorktreePath)
		if err != nil {
			return integrationMaterializationTransition{}, err
		}
		recoveryWorktree, recoveryErr := integrationWorktreeMatchesSnapshot(
			request.Target.WorktreePath, recoverySnapshot,
		)
		expectedWorktree, expectedErr := integrationWorktreeMatchesSnapshot(
			request.Target.WorktreePath, expectedSnapshot,
		)
		if recoveryErr != nil || expectedErr != nil {
			return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery worktree is unavailable")
		}
		if attached {
			if indexTree != transition.ExpectedTree || !expectedWorktree {
				return integrationMaterializationTransition{}, errors.New("apply integration candidate: attached recovery restoration differs")
			}
			break
		}
		switch {
		case indexTree == transition.RecoveryTree && recoveryWorktree:
			digest, digestErr := registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
			if digestErr != nil || digest != transition.RecoveryIndexDigest {
				return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery index changed")
			}
			if err := registry.authorizeRebaseFinalization(ctx, request, transition.ResultingHead); err != nil {
				return integrationMaterializationTransition{}, err
			}
			workspace, err := registry.integrationMaterializationWorkspace(ctx, request.Target.WorktreePath)
			if err != nil {
				return integrationMaterializationTransition{}, err
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"read-tree", "--reset", transition.ExpectedHead); err != nil {
				return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery index could not be restored")
			}
		case indexTree == transition.ExpectedTree && !expectedWorktree:
			if err := registry.authorizeRebaseFinalization(ctx, request, transition.ResultingHead); err != nil {
				return integrationMaterializationTransition{}, err
			}
			if err := materializeIntegrationWorktree(
				request.Target.WorktreePath, recoverySnapshot, expectedSnapshot,
			); err != nil {
				return integrationMaterializationTransition{}, err
			}
		case indexTree == transition.ExpectedTree && expectedWorktree:
			if err := registry.authorizeRebaseFinalization(ctx, request, transition.ResultingHead); err != nil {
				return integrationMaterializationTransition{}, err
			}
			if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
				request.Target.WorktreePath, "symbolic-ref", "HEAD", transition.TargetRef); err != nil {
				return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery target could not be reattached")
			}
		default:
			return integrationMaterializationTransition{}, errors.New("apply integration candidate: recovery restoration state is contradictory")
		}
	}
	if err := registry.removeSharedRebaseSequencer(ctx, request.Target.WorktreePath); err != nil {
		return integrationMaterializationTransition{}, err
	}
	expectedIndex, err := registry.expectedMaterializationIdentity(ctx, request, transition.TargetRef)
	if err != nil {
		return integrationMaterializationTransition{}, errors.New("apply integration candidate: restored recovery target is unverified")
	}
	pending := transition
	pending.Version = 1
	pending.State = "pending"
	pending.ExpectedIndexDigest = expectedIndex
	pending.RecoveryHead = ""
	pending.RecoveryTree = ""
	pending.RecoveryIndexDigest = ""
	if err := registry.replaceIntegrationMaterialization(request, transition, pending); err != nil {
		return integrationMaterializationTransition{}, err
	}
	return pending, nil
}

func (registry *Registry) recoveryMaterializationWorktreeMatchesIndex(
	ctx context.Context,
	worktreePath string,
) bool {
	tree, err := registry.integrationIndexTree(ctx, worktreePath)
	if err != nil {
		return false
	}
	snapshot, err := registry.loadIntegrationTreeSnapshot(ctx, worktreePath, tree)
	if err != nil {
		return false
	}
	matches, err := integrationWorktreeMatchesSnapshot(worktreePath, snapshot)
	return err == nil && matches
}

func (registry *Registry) removeSharedRebaseSequencer(ctx context.Context, worktreePath string) error {
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"rev-parse", "--absolute-git-dir")
	if err != nil || !filepath.IsAbs(gitDirectory) {
		return errors.New("apply integration candidate: recovery sequencer identity is unavailable")
	}
	root, err := os.OpenRoot(gitDirectory)
	if err != nil {
		return errors.New("apply integration candidate: recovery sequencer identity is unavailable")
	}
	defer root.Close()
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		info, err := root.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("apply integration candidate: recovery sequencer identity is invalid")
		}
		if err := root.RemoveAll(name); err != nil {
			return errors.New("apply integration candidate: recovery sequencer could not be retired")
		}
	}
	return nil
}

func (registry *Registry) replaceIntegrationMaterialization(
	request application.IntegrationAdapterRequest,
	previous integrationMaterializationTransition,
	next integrationMaterializationTransition,
) error {
	repository, err := registry.Resolve(request.Target.RepositoryID)
	if err != nil {
		return errors.New("apply integration candidate: materialization repository is unavailable")
	}
	directory, path, err := integrationMaterializationPath(repository, request)
	if err != nil {
		return err
	}
	current, found, err := readIntegrationMaterialization(path)
	if err != nil || !found || current != previous {
		return errors.New("apply integration candidate: materialization transition changed")
	}
	contents, err := json.Marshal(next)
	if err != nil {
		return errors.New("apply integration candidate: materialization transition cannot be encoded")
	}
	contents = append(contents, '\n')
	temporary := path + ".next"
	if err := discardServerRebaseProofTemporary(temporary); err != nil {
		return err
	}
	if err := createServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: materialization transition could not be advanced")
	}
	return syncDirectory(directory)
}
