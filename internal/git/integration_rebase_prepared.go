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

type preparedRebaseRestoration struct {
	Version        int    `json:"version"`
	OperationID    string `json:"operationId"`
	TargetRef      string `json:"targetRef"`
	ProofRef       string `json:"proofRef"`
	CandidateHead  string `json:"candidateHead"`
	CandidateTree  string `json:"candidateTree"`
	CandidateIndex string `json:"candidateIndex"`
	ExpectedHead   string `json:"expectedHead"`
	ExpectedTree   string `json:"expectedTree"`
	AdoptedBy      string `json:"adoptedBy,omitempty"`
}

func (registry *Registry) restorePreparedRebaseTarget(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	currentHead string,
	headRef string,
	attached bool,
) (bool, error) {
	repository, err := registry.Resolve(request.Target.RepositoryID)
	if err != nil {
		return false, errors.New("apply integration candidate: restoration repository is unavailable")
	}
	directory, restorationPath, err := preparedRebaseRestorationPath(repository, request)
	if err != nil {
		return false, err
	}
	restoration, found, err := readPreparedRebaseRestoration(restorationPath)
	if err != nil {
		return false, err
	}
	proofRef := integrationRebaseProofRef(request)
	if !found {
		if !attached || headRef != proofRef || currentHead != request.Candidate.HeadRevision {
			return false, nil
		}
		restoration, err = registry.prepareRebaseRestoration(ctx, request, targetRef, proofRef)
		if err != nil {
			return false, err
		}
		if err := ensureServerRebaseProofDirectory(repository.WorktreeRoot, directory); err != nil {
			return false, err
		}
		if err := publishPreparedRebaseRestoration(directory, restorationPath, restoration); err != nil {
			return false, err
		}
	} else if !preparedRebaseRestorationMatches(restoration, request, targetRef, proofRef) || restoration.AdoptedBy != "" {
		return false, errors.New("apply integration candidate: prepared restoration differs")
	}
	if err := registry.advancePreparedRebaseRestoration(ctx, request, request, restoration); err != nil {
		return false, withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.authorizePreparedRestoration(ctx, request, request, restoration); err != nil {
		return false, err
	}
	if err := retirePreparedRebaseRestoration(directory, restorationPath); err != nil {
		return false, err
	}
	return true, nil
}

func (registry *Registry) prepareRebaseRestoration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	proofRef string,
) (preparedRebaseRestoration, error) {
	if err := registry.ensureRebaseSequencerAbsent(ctx, request.Target.WorktreePath); err != nil {
		return preparedRebaseRestoration{}, err
	}
	proofHead, found, err := registry.integrationReceiptHeadAtPath(ctx, request.Target.WorktreePath, proofRef)
	if err != nil || !found || proofHead != request.Candidate.HeadRevision {
		return preparedRebaseRestoration{}, errors.New("apply integration candidate: prepared rebase proof differs")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || branchHead != request.Target.ExpectedHead {
		return preparedRebaseRestoration{}, errors.New("apply integration candidate: prepared rebase target differs")
	}
	clean, err := registry.integrationWorktreeCleanAtCommit(
		ctx, request.Target.WorktreePath, request.Candidate.HeadRevision,
	)
	if err != nil || !clean {
		return preparedRebaseRestoration{}, errors.New("apply integration candidate: prepared rebase is not clean")
	}
	candidateTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, request.Candidate.HeadRevision)
	if err != nil {
		return preparedRebaseRestoration{}, err
	}
	expectedTree, err := registry.integrationCommitTree(ctx, request.Target.WorktreePath, request.Target.ExpectedHead)
	if err != nil {
		return preparedRebaseRestoration{}, err
	}
	candidate, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, candidateTree)
	if err != nil {
		return preparedRebaseRestoration{}, err
	}
	expected, err := registry.loadIntegrationTreeSnapshot(ctx, request.Target.WorktreePath, expectedTree)
	if err != nil {
		return preparedRebaseRestoration{}, err
	}
	if err := validateIntegrationMaterializationTopology(candidate, expected); err != nil {
		return preparedRebaseRestoration{}, err
	}
	indexDigest, err := registry.integrationIndexDigest(ctx, request.Target.WorktreePath)
	if err != nil {
		return preparedRebaseRestoration{}, err
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return preparedRebaseRestoration{}, err
	}
	if err := registry.validateIntegrationMutationDeadline(request); err != nil {
		return preparedRebaseRestoration{}, err
	}
	return preparedRebaseRestoration{
		Version: 1, OperationID: request.OperationID, TargetRef: targetRef, ProofRef: proofRef,
		CandidateHead: request.Candidate.HeadRevision, CandidateTree: candidateTree,
		CandidateIndex: indexDigest, ExpectedHead: request.Target.ExpectedHead, ExpectedTree: expectedTree,
	}, nil
}

func (registry *Registry) advancePreparedRebaseRestoration(
	ctx context.Context,
	authority application.IntegrationAdapterRequest,
	identity application.IntegrationAdapterRequest,
	restoration preparedRebaseRestoration,
) error {
	candidateTree, candidateTreeErr := registry.integrationCommitTree(
		ctx, identity.Target.WorktreePath, restoration.CandidateHead,
	)
	expectedTree, expectedTreeErr := registry.integrationCommitTree(
		ctx, identity.Target.WorktreePath, restoration.ExpectedHead,
	)
	branchHead, branchErr := registry.integrationBranchHead(ctx, identity.Target.WorktreePath, restoration.TargetRef)
	proofHead, proofFound, proofErr := registry.integrationReceiptHeadAtPath(
		ctx, identity.Target.WorktreePath, restoration.ProofRef,
	)
	if candidateTreeErr != nil || expectedTreeErr != nil || branchErr != nil || proofErr != nil || !proofFound ||
		candidateTree != restoration.CandidateTree || expectedTree != restoration.ExpectedTree ||
		branchHead != restoration.ExpectedHead || proofHead != restoration.CandidateHead {
		return errors.New("apply integration candidate: prepared restoration proof differs")
	}
	candidate, err := registry.loadIntegrationTreeSnapshot(
		ctx, identity.Target.WorktreePath, restoration.CandidateTree,
	)
	if err != nil {
		return err
	}
	expected, err := registry.loadIntegrationTreeSnapshot(ctx, identity.Target.WorktreePath, restoration.ExpectedTree)
	if err != nil {
		return err
	}
	for {
		currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
			identity.Target.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
		if err != nil {
			return errors.New("apply integration candidate: prepared restoration head is unavailable")
		}
		headRef, attached, err := registry.integrationHeadRef(ctx, identity.Target.WorktreePath)
		if err != nil || !attached || headRef != restoration.ProofRef && headRef != restoration.TargetRef {
			return errors.New("apply integration candidate: prepared restoration attachment differs")
		}
		indexTree, err := registry.integrationIndexTree(ctx, identity.Target.WorktreePath)
		if err != nil {
			return err
		}
		candidateWorktree, candidateErr := integrationWorktreeMatchesSnapshot(identity.Target.WorktreePath, candidate)
		expectedWorktree, expectedErr := integrationWorktreeMatchesSnapshot(identity.Target.WorktreePath, expected)
		if candidateErr != nil || expectedErr != nil {
			return errors.New("apply integration candidate: prepared restoration worktree is unavailable")
		}
		if headRef == restoration.TargetRef {
			if currentHead != restoration.ExpectedHead || indexTree != restoration.ExpectedTree || !expectedWorktree {
				return errors.New("apply integration candidate: completed prepared restoration differs")
			}
			return nil
		}
		if currentHead != restoration.CandidateHead {
			return errors.New("apply integration candidate: prepared restoration head differs")
		}
		switch {
		case indexTree == restoration.CandidateTree && candidateWorktree:
			digest, digestErr := registry.integrationIndexDigest(ctx, identity.Target.WorktreePath)
			if digestErr != nil || digest != restoration.CandidateIndex {
				return errors.New("apply integration candidate: prepared restoration index differs")
			}
			if err := registry.authorizePreparedRestoration(ctx, authority, identity, restoration); err != nil {
				return err
			}
			workspace, err := registry.integrationMaterializationWorkspace(ctx, identity.Target.WorktreePath)
			if err != nil {
				return err
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"read-tree", "--reset", restoration.ExpectedHead); err != nil {
				return errors.New("apply integration candidate: prepared rebase index could not be restored")
			}
			if err := registry.authorizePreparedRestoration(ctx, authority, identity, restoration); err != nil {
				return err
			}
		case indexTree == restoration.ExpectedTree && !expectedWorktree:
			if err := registry.authorizePreparedRestoration(ctx, authority, identity, restoration); err != nil {
				return err
			}
			if err := materializeIntegrationWorktree(identity.Target.WorktreePath, candidate, expected); err != nil {
				return err
			}
			if err := registry.authorizePreparedRestoration(ctx, authority, identity, restoration); err != nil {
				return err
			}
		case indexTree == restoration.ExpectedTree && expectedWorktree:
			if err := registry.authorizePreparedRestoration(ctx, authority, identity, restoration); err != nil {
				return err
			}
			if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
				identity.Target.WorktreePath, "symbolic-ref", "HEAD", restoration.TargetRef); err != nil {
				return errors.New("apply integration candidate: prepared rebase target could not be restored")
			}
			if err := registry.authorizePreparedRestoration(ctx, authority, identity, restoration); err != nil {
				return err
			}
		default:
			return errors.New("apply integration candidate: prepared restoration state is contradictory")
		}
	}
}

func (registry *Registry) authorizePreparedRestoration(
	ctx context.Context,
	authority application.IntegrationAdapterRequest,
	identity application.IntegrationAdapterRequest,
	restoration preparedRebaseRestoration,
) error {
	if err := registry.validateIntegrationExecutionPolicy(ctx, authority); err != nil {
		return withoutIntegrationMutationNotStarted(err)
	}
	if err := registry.validateIntegrationMutationDeadline(authority); err != nil {
		return withoutIntegrationMutationNotStarted(err)
	}
	return registry.validatePreparedRestorationReceiptFamily(ctx, authority, identity, restoration)
}

func preparedRebaseRestorationPath(
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, string, error) {
	reference := integrationReceiptRef("restoration", request)
	digest := strings.TrimPrefix(reference, "refs/comis/integration/restoration/")
	if len(digest) != 64 || !lowerHex(digest) {
		return "", "", errors.New("apply integration candidate: prepared restoration identity is invalid")
	}
	directory := filepath.Join(repository.WorktreeRoot, ".comis-integration-proofs")
	return directory, filepath.Join(directory, "restoration-"+digest), nil
}

func preparedRebaseRestorationMatches(
	restoration preparedRebaseRestoration,
	request application.IntegrationAdapterRequest,
	targetRef string,
	proofRef string,
) bool {
	return restoration.Version == 1 && restoration.OperationID == request.OperationID &&
		restoration.TargetRef == targetRef && restoration.ProofRef == proofRef &&
		restoration.CandidateHead == request.Candidate.HeadRevision &&
		restoration.ExpectedHead == request.Target.ExpectedHead &&
		gitRevisionPattern.MatchString(restoration.CandidateTree) &&
		gitRevisionPattern.MatchString(restoration.ExpectedTree) &&
		len(restoration.CandidateIndex) == 64 && lowerHex(restoration.CandidateIndex)
}

func retirePreparedRebaseRestoration(directory string, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("apply integration candidate: prepared restoration could not be retired")
	}
	return syncDirectory(directory)
}

func publishPreparedRebaseRestoration(
	directory string,
	path string,
	restoration preparedRebaseRestoration,
) error {
	contents, err := encodePreparedRebaseRestoration(restoration)
	if err != nil {
		return err
	}
	temporary := path + ".pending"
	if err := discardServerRebaseProofTemporary(temporary); err != nil {
		return err
	}
	if err := createServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: prepared restoration could not be published")
	}
	return syncDirectory(directory)
}

func encodePreparedRebaseRestoration(restoration preparedRebaseRestoration) ([]byte, error) {
	contents, err := json.Marshal(restoration)
	if err != nil {
		return nil, errors.New("apply integration candidate: prepared restoration cannot be encoded")
	}
	return append(contents, '\n'), nil
}

func readPreparedRebaseRestoration(path string) (preparedRebaseRestoration, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return preparedRebaseRestoration{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 2 || info.Size() > 4096 {
		return preparedRebaseRestoration{}, false,
			errors.New("apply integration candidate: prepared restoration is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return preparedRebaseRestoration{}, false,
			errors.New("apply integration candidate: prepared restoration is unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > 4096 {
		return preparedRebaseRestoration{}, false,
			errors.New("apply integration candidate: prepared restoration is unavailable")
	}
	var restoration preparedRebaseRestoration
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&restoration) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return preparedRebaseRestoration{}, false,
			errors.New("apply integration candidate: prepared restoration is malformed")
	}
	return restoration, true, nil
}
