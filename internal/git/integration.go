package git

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const integrationZeroRevision = "0000000000000000000000000000000000000000"

// ApplyIntegrationCandidate revalidates both task worktrees under the registry
// mutation lock and executes only one fixed strategy vocabulary. Git refs form
// content-free receipts for exact replay between Git mutation and SQLite commit.
func (registry *Registry) ApplyIntegrationCandidate(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, error) {
	if registry == nil || ctx == nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := validateIntegrationRequest(request); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	repository, err := registry.Resolve(request.Target.RepositoryID)
	if err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: repository is unavailable")
	}
	if err := registry.preflightIntegrationWorktrees(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	appliedRef := integrationReceiptRef("applied", request)
	conflictedRef := integrationReceiptRef("conflicted", request)
	if replay, found, err := registry.replayAppliedIntegration(ctx, request, repository, appliedRef); err != nil || found {
		return replay, err
	}
	if replay, found, err := registry.replayConflictedIntegration(ctx, request, repository, conflictedRef); err != nil || found {
		return replay, err
	}
	if replay, found, err := registry.reconcileCompletedIntegrationPlan(ctx, repository, request); err != nil || found {
		return replay, err
	}
	if request.ReceiptOnly {
		if replay, found, err := registry.reconcileReceiptOnlyCompletedRebase(ctx, repository, request); err != nil || found {
			return replay, err
		}
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: mutation authority is unavailable")
	}
	if replay, found, err := registry.reconcileInterruptedRebase(ctx, request, repository, conflictedRef); err != nil || found {
		return replay, err
	}
	if request.RecoveryOperationID != "" {
		return registry.resumeRebaseIntegration(ctx, request, repository)
	}
	target, candidate, err := registry.inspectIntegrationInputs(ctx, request, repository)
	if err != nil {
		return application.IntegrationAdapterResult{}, err
	}
	expectedBranch := expectedIntegrationTargetBranch(request)
	if target.Cleanliness != CandidateClean || target.HeadRevision != request.Target.ExpectedHead || target.Branch != expectedBranch {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: target head or cleanliness changed")
	}
	if candidate.Cleanliness != CandidateClean || candidate.HeadRevision != request.Candidate.HeadRevision {
		return application.IntegrationAdapterResult{
			Outcome: application.IntegrationInvalidated, PreviousHead: request.Target.ExpectedHead,
		}, nil
	}
	mutationAt := registry.clock().UTC()
	if mutationAt.IsZero() || !mutationAt.Before(request.EvidenceExpiresAt) {
		return application.IntegrationAdapterResult{}, fmt.Errorf(
			"apply integration candidate: candidate evidence expired before mutation: %w",
			application.ErrIntegrationMutationNotStarted,
		)
	}
	if err := registry.validateIntegrationExecutionPolicy(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, errors.Join(err, application.ErrIntegrationMutationNotStarted)
	}
	if err := registry.runIntegrationStrategy(ctx, request, repository); err != nil {
		if errors.Is(err, application.ErrIntegrationMutationNotStarted) {
			return application.IntegrationAdapterResult{}, err
		}
		conflicts, conflictErr := registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
		if conflictErr != nil || len(conflicts) == 0 {
			if ctx.Err() != nil {
				return application.IntegrationAdapterResult{}, ctx.Err()
			}
			return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: strategy failed without attributable conflicts")
		}
		if request.Strategy == application.IntegrationRebase {
			if proofErr := registry.recordServerRebaseConflict(ctx, repository, request); proofErr != nil {
				return application.IntegrationAdapterResult{}, proofErr
			}
		}
		if receiptErr := registry.createIntegrationReceipt(ctx, repository, conflictedRef, request.Target.ExpectedHead); receiptErr != nil {
			return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: conflict receipt could not be recorded")
		}
		return application.IntegrationAdapterResult{
			Outcome: application.IntegrationConflicted, PreviousHead: request.Target.ExpectedHead,
			ConflictPaths: conflicts,
		}, nil
	}

	final, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || final.Cleanliness != CandidateClean || final.HeadRevision == request.Target.ExpectedHead ||
		final.Branch != expectedBranch {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: resulting target is unverified")
	}
	if err := registry.createIntegrationReceipt(ctx, repository, appliedRef, final.HeadRevision); err != nil {
		return application.IntegrationAdapterResult{}, errors.New("apply integration candidate: applied receipt could not be recorded")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead,
		ResultingHead: final.HeadRevision,
	}, nil
}

func validateIntegrationRequest(request application.IntegrationAdapterRequest) error {
	validStrategy := request.Strategy == application.IntegrationMerge || request.Strategy == application.IntegrationRebase ||
		request.Strategy == application.IntegrationCherryPick
	if domain.ValidateOperationID(request.OperationID) != nil ||
		(request.RecoveryOperationID != "" && (domain.ValidateOperationID(request.RecoveryOperationID) != nil ||
			request.RecoveryOperationID == request.OperationID)) || !validStrategy ||
		domain.ValidateTaskHandle(request.Target.TaskHandle) != nil || domain.ValidateTaskHandle(request.Candidate.TaskHandle) != nil ||
		request.Target.TaskHandle == request.Candidate.TaskHandle || !repositoryIDPattern.MatchString(request.Target.RepositoryID) ||
		request.Target.RepositoryID != request.Candidate.RepositoryID || request.Target.WorktreePath == request.Candidate.WorktreePath ||
		!gitRevisionPattern.MatchString(request.Target.ExpectedHead) || !gitRevisionPattern.MatchString(request.Candidate.BaseRevision) ||
		!gitRevisionPattern.MatchString(request.Candidate.HeadRevision) ||
		domain.ValidateOperationID(request.Target.PreparationOperationID) != nil ||
		request.EvidenceExpiresAt.IsZero() || request.EvidenceExpiresAt.Location() != time.UTC {
		return errors.New("apply integration candidate: request is invalid")
	}
	return nil
}

func (registry *Registry) inspectIntegrationInputs(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) (CandidateSnapshot, CandidateSnapshot, error) {
	base, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-parse", "--verify", request.Candidate.BaseRevision+"^{commit}")
	if err != nil || base != request.Candidate.BaseRevision {
		return CandidateSnapshot{}, CandidateSnapshot{}, errors.New("apply integration candidate: candidate base is unavailable")
	}
	descended, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"merge-base", "--is-ancestor", request.Candidate.BaseRevision, request.Candidate.HeadRevision)
	if err != nil || !descended || request.Candidate.BaseRevision == request.Candidate.HeadRevision {
		return CandidateSnapshot{}, CandidateSnapshot{}, errors.New("apply integration candidate: candidate ancestry is invalid")
	}
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil {
		return CandidateSnapshot{}, CandidateSnapshot{}, errors.New("apply integration candidate: target worktree is unavailable")
	}
	candidate, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Candidate.TaskHandle, RepositoryID: request.Candidate.RepositoryID,
		WorktreePath: request.Candidate.WorktreePath,
	})
	if err != nil {
		return CandidateSnapshot{}, CandidateSnapshot{}, errors.New("apply integration candidate: candidate worktree is unavailable")
	}
	return target, candidate, nil
}

func expectedIntegrationTargetBranch(request application.IntegrationAdapterRequest) string {
	branch, _ := preparedBranch(
		request.Target.RepositoryID, request.Target.TaskHandle, request.Target.PreparationOperationID,
	)
	return branch
}

func (registry *Registry) restorePreparedRebaseTarget(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	targetRef string,
	currentHead string,
	headRef string,
	attached bool,
) (bool, error) {
	proofRef := integrationRebaseProofRef(request)
	if !attached || headRef != proofRef || currentHead != request.Candidate.HeadRevision {
		return false, nil
	}
	if err := registry.ensureRebaseSequencerAbsent(ctx, request.Target.WorktreePath); err != nil {
		return false, err
	}
	proofHead, found, err := registry.integrationReceiptHeadAtPath(ctx, request.Target.WorktreePath, proofRef)
	if err != nil || !found || proofHead != request.Candidate.HeadRevision {
		return false, errors.New("apply integration candidate: prepared rebase proof differs")
	}
	branchHead, err := registry.integrationBranchHead(ctx, request.Target.WorktreePath, targetRef)
	if err != nil || branchHead != request.Target.ExpectedHead {
		return false, errors.New("apply integration candidate: prepared rebase target differs")
	}
	status, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || len(status) != 0 {
		return false, errors.New("apply integration candidate: prepared rebase is not clean")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"-c", "core.hooksPath=/dev/null", "checkout", "--no-guess", strings.TrimPrefix(targetRef, "refs/heads/")); err != nil {
		return false, errors.New("apply integration candidate: prepared rebase target could not be restored")
	}
	return true, nil
}

func (registry *Registry) integrationConflictPaths(ctx context.Context, worktreePath string) ([]string, error) {
	encoded, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"diff", "--name-only", "--diff-filter=U", "-z")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(encoded), "\x00")
	paths := make([]string, 0, len(parts))
	for _, path := range parts {
		if path == "" {
			continue
		}
		if strings.ContainsAny(path, "\r\n\x00") || len([]byte(path)) > 1024 || len(paths) == 256 {
			return nil, errors.New("apply integration candidate: conflict paths exceed their bound")
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, nil
}

func integrationReceiptRef(outcome string, request application.IntegrationAdapterRequest) string {
	canonical, _ := json.Marshal(request)
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("refs/comis/integration/%s/%x", outcome, digest)
}

func integrationRebaseProofRef(request application.IntegrationAdapterRequest) string {
	if request.RecoveryOperationID != "" {
		request.OperationID = request.RecoveryOperationID
		request.RecoveryOperationID = ""
	}
	canonical, _ := json.Marshal(request)
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("refs/heads/comis-integration-proof-%x", digest)
}

func (registry *Registry) createIntegrationReceipt(
	ctx context.Context,
	repository Repository,
	reference string,
	head string,
) error {
	_, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"update-ref", reference, head, integrationZeroRevision)
	return err
}

func (registry *Registry) validateIntegrationMutationDeadline(
	request application.IntegrationAdapterRequest,
) error {
	mutationAt := registry.clock().UTC()
	if mutationAt.IsZero() || !mutationAt.Before(request.EvidenceExpiresAt) {
		return errors.Join(
			errors.New("apply integration candidate: candidate evidence expired before mutation"),
			application.ErrIntegrationMutationNotStarted,
		)
	}
	return nil
}

type integrationReceiptKind uint8

const (
	integrationReceiptAbsent integrationReceiptKind = iota
	integrationReceiptDirect
	integrationReceiptSymbolic
)

type inspectedIntegrationReceipt struct {
	kind  integrationReceiptKind
	value string
}

func (registry *Registry) inspectIntegrationReceipt(
	ctx context.Context,
	worktreePath string,
	reference string,
) (inspectedIntegrationReceipt, error) {
	output, exitCode, err := executeGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"symbolic-ref", "--quiet", "--no-recurse", reference)
	if err != nil {
		return inspectedIntegrationReceipt{}, err
	}
	if exitCode == 0 {
		target := strings.TrimSuffix(string(output), "\n")
		if target == "" || strings.ContainsAny(target, "\x00\r\n\t ") {
			return inspectedIntegrationReceipt{}, errors.New("apply integration candidate: symbolic receipt is invalid")
		}
		return inspectedIntegrationReceipt{kind: integrationReceiptSymbolic, value: target}, nil
	}
	if exitCode != 1 {
		return inspectedIntegrationReceipt{}, errors.New("apply integration candidate: symbolic receipt inspection failed")
	}
	_, exitCode, err = executeGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"show-ref", "--verify", "--quiet", reference)
	if err != nil {
		return inspectedIntegrationReceipt{}, err
	}
	if exitCode == 1 {
		return inspectedIntegrationReceipt{kind: integrationReceiptAbsent}, nil
	}
	if exitCode != 0 {
		return inspectedIntegrationReceipt{}, errors.New("apply integration candidate: direct receipt inspection failed")
	}
	head, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
		"show-ref", "--verify", "--hash", reference)
	if err != nil || !gitRevisionPattern.MatchString(head) {
		return inspectedIntegrationReceipt{}, errors.New("apply integration candidate: direct receipt is invalid")
	}
	return inspectedIntegrationReceipt{kind: integrationReceiptDirect, value: head}, nil
}

func (registry *Registry) replayAppliedIntegration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
	reference string,
) (application.IntegrationAdapterResult, bool, error) {
	head, found, err := registry.integrationReceiptHead(ctx, repository, reference)
	if err != nil || !found {
		return application.IntegrationAdapterResult{}, false, err
	}
	if request.Strategy == application.IntegrationRebase {
		if err := registry.requireServerRebaseProof(ctx, repository, request, head); err != nil {
			return application.IntegrationAdapterResult{}, false, err
		}
	}
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || target.Cleanliness != CandidateClean || target.HeadRevision != head ||
		target.Branch != expectedIntegrationTargetBranch(request) {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: applied receipt differs from target")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Target.ExpectedHead, ResultingHead: head,
	}, true, nil
}

func (registry *Registry) replayConflictedIntegration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
	reference string,
) (application.IntegrationAdapterResult, bool, error) {
	head, found, err := registry.integrationReceiptHead(ctx, repository, reference)
	if err != nil || !found {
		return application.IntegrationAdapterResult{}, false, err
	}
	if head != request.Target.ExpectedHead {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: conflict receipt head differs")
	}
	if request.Strategy == application.IntegrationRebase {
		if _, err := registry.integrationTargetRef(ctx, request); err != nil {
			return application.IntegrationAdapterResult{}, false, err
		}
		return registry.replayConflictedRebase(ctx, request, head)
	}
	target, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.Target.TaskHandle, RepositoryID: request.Target.RepositoryID,
		WorktreePath: request.Target.WorktreePath,
	})
	if err != nil || target.HeadRevision != head || target.Branch != expectedIntegrationTargetBranch(request) {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: conflict receipt differs from target")
	}
	conflicts, err := registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
	if err != nil || len(conflicts) == 0 {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: recorded conflicts are unavailable")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationConflicted, PreviousHead: head, ConflictPaths: conflicts,
	}, true, nil
}

func (registry *Registry) replayConflictedRebase(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	head string,
) (application.IntegrationAdapterResult, bool, error) {
	if err := registry.validateRebaseOrigin(ctx, request); err != nil {
		return application.IntegrationAdapterResult{}, false, err
	}
	rebaseHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: recorded rebase is unavailable")
	}
	baseContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", request.Candidate.BaseRevision, rebaseHead)
	if err != nil || !baseContains {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: rebase conflict is outside candidate range")
	}
	candidateContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", rebaseHead, request.Candidate.HeadRevision)
	if err != nil || !candidateContains {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: rebase conflict differs from candidate")
	}
	currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: rebasing target head is unavailable")
	}
	targetContains, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"merge-base", "--is-ancestor", head, currentHead)
	if err != nil || !targetContains {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: rebasing target differs from receipt")
	}
	conflicts, err := registry.integrationConflictPaths(ctx, request.Target.WorktreePath)
	if err != nil || len(conflicts) == 0 {
		return application.IntegrationAdapterResult{}, false, errors.New("apply integration candidate: recorded conflicts are unavailable")
	}
	return application.IntegrationAdapterResult{
		Outcome: application.IntegrationConflicted, PreviousHead: head, ConflictPaths: conflicts,
	}, true, nil
}

func (registry *Registry) integrationReceiptHead(
	ctx context.Context,
	repository Repository,
	reference string,
) (string, bool, error) {
	return registry.integrationReceiptHeadAtPath(ctx, repository.PrimaryCheckout, reference)
}

func (registry *Registry) integrationReceiptHeadAtPath(
	ctx context.Context,
	worktreePath string,
	reference string,
) (string, bool, error) {
	receipt, err := registry.inspectIntegrationReceipt(ctx, worktreePath, reference)
	if err != nil {
		return "", false, err
	}
	switch receipt.kind {
	case integrationReceiptAbsent:
		return "", false, nil
	case integrationReceiptSymbolic:
		return "", false, errors.New("apply integration candidate: receipt is symbolic")
	case integrationReceiptDirect:
		objectType, typeErr := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", worktreePath,
			"cat-file", "-t", receipt.value)
		if typeErr != nil || objectType != "commit" {
			return "", false, errors.New("apply integration candidate: receipt is invalid")
		}
		return receipt.value, true, nil
	default:
		return "", false, errors.New("apply integration candidate: receipt is invalid")
	}
}

var _ application.IntegrationAdapter = (*Registry)(nil)
