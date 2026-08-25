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

type serverIntegrationPlan struct {
	OperationID   string                          `json:"operationId"`
	Strategy      application.IntegrationStrategy `json:"strategy"`
	ExpectedHead  string                          `json:"expectedHead"`
	CandidateBase string                          `json:"candidateBase"`
	CandidateHead string                          `json:"candidateHead"`
	ResultingHead string                          `json:"resultingHead"`
}

var errIntegrationSharedStateWritten = errors.New("apply integration candidate: shared integration state may have changed")

func (registry *Registry) runIsolatedIntegration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) (serverIntegrationPlan, bool, error) {
	directory, path, err := serverIntegrationPlanPath(repository, request)
	if err != nil {
		return serverIntegrationPlan{}, false, err
	}
	if err := ensureServerRebaseProofDirectory(repository.WorktreeRoot, directory); err != nil {
		return serverIntegrationPlan{}, false, err
	}
	if existing, found, err := readServerIntegrationPlan(path); err != nil {
		return serverIntegrationPlan{}, false, err
	} else if found {
		if !serverIntegrationPlanMatches(existing, request) {
			return serverIntegrationPlan{}, false, errors.New("apply integration candidate: isolated operation proof differs")
		}
		return existing, false, nil
	}
	var plan serverIntegrationPlan
	conflicted := false
	sharedStateWritten := false
	err = registry.withIsolatedRebaseWorkspace(ctx, request.Target.WorktreePath, directory,
		func(workspace gitWorkspaceEnvironment) error {
			branch := "refs/heads/integration-result"
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"update-ref", branch, request.Target.ExpectedHead); err != nil {
				return errors.New("apply integration candidate: isolated target is unavailable")
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"symbolic-ref", "HEAD", branch); err != nil {
				return errors.New("apply integration candidate: isolated target attachment is unavailable")
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"reset", "--hard", request.Target.ExpectedHead); err != nil {
				return errors.New("apply integration candidate: isolated target checkout is unavailable")
			}
			arguments := isolatedIntegrationMutationConfig()
			switch request.Strategy {
			case application.IntegrationMerge:
				arguments = append(arguments, "merge", "--no-ff", "--no-edit", "--no-verify", "--no-stat", request.Candidate.HeadRevision)
			case application.IntegrationCherryPick:
				arguments = append(arguments, "cherry-pick", request.Candidate.BaseRevision+".."+request.Candidate.HeadRevision)
			default:
				return errors.New("apply integration candidate: isolated strategy is invalid")
			}
			_, exitCode, err := executeGitWithEnvironmentAndOutputLimit(
				ctx, registry.gitExecutable, &workspace, maximumGitOutputBytes, arguments...,
			)
			if err != nil {
				return errors.New("apply integration candidate: isolated strategy is unavailable")
			}
			if exitCode == 1 {
				paths, pathErr := rebaseIndexOutput(ctx, registry.gitExecutable, workspace,
					"diff", "--name-only", "--diff-filter=U", "-z")
				if pathErr != nil || len(paths) == 0 {
					return errors.New("apply integration candidate: isolated strategy failed without conflicts")
				}
				conflicted = true
				return nil
			}
			if exitCode != 0 {
				return errors.New("apply integration candidate: isolated strategy failed")
			}
			resultingHead, err := runGitInWorkspace(ctx, registry.gitExecutable, workspace,
				"rev-parse", "--verify", "HEAD^{commit}")
			if err != nil || resultingHead == request.Target.ExpectedHead {
				return errors.New("apply integration candidate: isolated result is unavailable")
			}
			if err := registry.validateIsolatedIntegrationResult(ctx, workspace, request, repository, resultingHead); err != nil {
				return err
			}
			if err := registry.validateIsolatedMaterializationTopology(
				ctx, workspace, request.Target.ExpectedHead, resultingHead,
			); err != nil {
				return err
			}
			if err := registry.validateLiveMaterializationBaseBeforeImport(ctx, request); err != nil {
				return err
			}
			attempted, err := importIsolatedGitObjectsWithAuthorityState(
				workspace.gitObjectDirectory, workspace.gitAlternateObjectDirectory,
			)
			sharedStateWritten = sharedStateWritten || attempted
			if err != nil {
				return err
			}
			plan = serverIntegrationPlan{
				OperationID: request.OperationID, Strategy: request.Strategy,
				ExpectedHead: request.Target.ExpectedHead, CandidateBase: request.Candidate.BaseRevision,
				CandidateHead: request.Candidate.HeadRevision, ResultingHead: resultingHead,
			}
			return nil
		})
	if err != nil || conflicted {
		if err != nil && sharedStateWritten {
			err = errors.Join(err, errIntegrationSharedStateWritten)
		}
		return serverIntegrationPlan{}, conflicted, err
	}
	if err := publishServerIntegrationPlan(directory, path, plan); err != nil {
		if sharedStateWritten {
			err = errors.Join(err, errIntegrationSharedStateWritten)
		}
		return serverIntegrationPlan{}, false, err
	}
	return plan, false, nil
}

func isolatedIntegrationMutationConfig() []string {
	return []string{
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "commit.gpgSign=false",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
	}
}

func (registry *Registry) validateIsolatedIntegrationResult(
	ctx context.Context,
	workspace gitWorkspaceEnvironment,
	request application.IntegrationAdapterRequest,
	repository Repository,
	resultingHead string,
) error {
	switch request.Strategy {
	case application.IntegrationMerge:
		parents, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
			"rev-list", "--parents", "-n", "1", resultingHead)
		fields := strings.Fields(string(parents))
		if err != nil || len(fields) != 3 || fields[0] != resultingHead ||
			fields[1] != request.Target.ExpectedHead || fields[2] != request.Candidate.HeadRevision {
			return errors.New("apply integration candidate: isolated merge result differs")
		}
	case application.IntegrationCherryPick:
		candidates, err := registry.rebaseCommitRange(ctx, repository,
			request.Candidate.BaseRevision, request.Candidate.HeadRevision)
		if err != nil {
			return errors.New("apply integration candidate: isolated cherry-pick range is unavailable")
		}
		resultsOutput, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
			"rev-list", "--reverse", request.Target.ExpectedHead+".."+resultingHead)
		results := strings.Fields(string(resultsOutput))
		if err != nil || len(candidates) != len(results) {
			return errors.New("apply integration candidate: isolated cherry-pick dropped commits")
		}
		for index := range candidates {
			candidatePatch, candidateErr := registry.rebasePatchIdentity(ctx, repository, candidates[index])
			resultPatch, resultErr := registry.rebasePatchIdentityInWorkspace(ctx, workspace, results[index])
			if candidateErr != nil || resultErr != nil || candidatePatch != resultPatch {
				return errors.New("apply integration candidate: isolated cherry-pick content differs")
			}
		}
	default:
		return errors.New("apply integration candidate: isolated strategy is invalid")
	}
	return nil
}

func serverIntegrationPlanPath(
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, string, error) {
	reference := integrationReceiptRef("plan", request)
	digest := strings.TrimPrefix(reference, "refs/comis/integration/plan/")
	if len(digest) != 64 || !lowerHex(digest) {
		return "", "", errors.New("apply integration candidate: isolated operation identity is invalid")
	}
	directory := filepath.Join(repository.WorktreeRoot, ".comis-integration-proofs")
	return directory, filepath.Join(directory, "plan-"+digest), nil
}

func publishServerIntegrationPlan(directory, path string, plan serverIntegrationPlan) error {
	contents, err := json.Marshal(plan)
	if err != nil {
		return errors.New("apply integration candidate: isolated operation proof cannot be encoded")
	}
	contents = append(contents, '\n')
	if existing, found, err := readServerIntegrationPlan(path); err != nil {
		return err
	} else if found {
		if existing != plan {
			return errors.New("apply integration candidate: isolated operation proof differs")
		}
		return nil
	}
	temporary := path + ".pending"
	if err := discardServerRebaseProofTemporary(temporary); err != nil {
		return err
	}
	if err := createServerRebaseProof(temporary, contents); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return errors.New("apply integration candidate: isolated operation proof could not be published")
	}
	return syncDirectory(directory)
}

func readServerIntegrationPlan(path string) (serverIntegrationPlan, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return serverIntegrationPlan{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() < 2 || info.Size() > 4096 {
		return serverIntegrationPlan{}, false, errors.New("apply integration candidate: isolated operation proof is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return serverIntegrationPlan{}, false, errors.New("apply integration candidate: isolated operation proof is unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, 4097))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > 4096 {
		return serverIntegrationPlan{}, false, errors.New("apply integration candidate: isolated operation proof is unavailable")
	}
	var plan serverIntegrationPlan
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&plan) != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		plan.OperationID == "" || !validIntegrationPlanStrategy(plan.Strategy) ||
		!gitRevisionPattern.MatchString(plan.ExpectedHead) || !gitRevisionPattern.MatchString(plan.CandidateBase) ||
		!gitRevisionPattern.MatchString(plan.CandidateHead) || !gitRevisionPattern.MatchString(plan.ResultingHead) {
		return serverIntegrationPlan{}, false, errors.New("apply integration candidate: isolated operation proof is malformed")
	}
	return plan, true, nil
}

func validIntegrationPlanStrategy(strategy application.IntegrationStrategy) bool {
	return strategy == application.IntegrationMerge || strategy == application.IntegrationCherryPick
}

func serverIntegrationPlanMatches(plan serverIntegrationPlan, request application.IntegrationAdapterRequest) bool {
	return plan.OperationID == request.OperationID && plan.Strategy == request.Strategy &&
		plan.ExpectedHead == request.Target.ExpectedHead && plan.CandidateBase == request.Candidate.BaseRevision &&
		plan.CandidateHead == request.Candidate.HeadRevision && plan.ResultingHead != plan.ExpectedHead
}

func (registry *Registry) reconcileCompletedIntegrationPlan(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, bool, error) {
	if request.Strategy == application.IntegrationRebase {
		return application.IntegrationAdapterResult{}, false, nil
	}
	_, path, err := serverIntegrationPlanPath(repository, request)
	if err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	plan, found, err := readServerIntegrationPlan(path)
	if err != nil || !found {
		return application.IntegrationAdapterResult{}, false, err
	}
	if !serverIntegrationPlanMatches(plan, request) {
		return application.IntegrationAdapterResult{}, true, errors.New("apply integration candidate: isolated operation proof differs")
	}
	targetRef := "refs/heads/" + expectedIntegrationTargetBranch(request)
	if found, err := registry.reconcileIntegrationMaterialization(
		ctx, request, targetRef, plan.ResultingHead,
	); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	} else if found {
		if err := registry.validateCompletedIntegrationReceiptFamily(ctx, request); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		target, inspectErr := registry.inspectIntegrationCandidate(ctx, CandidateSnapshotRequest{
			TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
			WorktreePath: request.Target.WorktreePath,
		})
		if inspectErr != nil || target.HeadRevision != plan.ResultingHead || target.Cleanliness != CandidateClean ||
			target.Branch != expectedIntegrationTargetBranch(request) {
			return application.IntegrationAdapterResult{}, true,
				errors.New("apply integration candidate: completed materialization is unverified")
		}
		return application.IntegrationAdapterResult{
			Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
			ResultingHead: plan.ResultingHead,
		}, true, nil
	}
	if err := registry.validateIntegrationMaterializationResult(ctx, request, plan.ResultingHead); err != nil {
		return application.IntegrationAdapterResult{}, true,
			errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	if request.ReceiptOnly {
		if err := registry.provePristineIntegrationState(ctx, request, targetRef); err != nil {
			return application.IntegrationAdapterResult{}, true, err
		}
		return application.IntegrationAdapterResult{}, true, errors.Join(
			errors.New("apply integration candidate: isolated plan has no shared mutation"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	target, err := registry.inspectIntegrationCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || target.HeadRevision != plan.ResultingHead || target.Cleanliness != CandidateClean ||
		target.Branch != expectedIntegrationTargetBranch(request) {
		return application.IntegrationAdapterResult{}, false, nil
	}
	if err := registry.validateCompletedIntegrationReceiptFamily(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, true, err
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: plan.ResultingHead,
	}, true, nil
}
