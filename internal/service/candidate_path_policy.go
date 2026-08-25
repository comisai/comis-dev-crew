package service

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

const candidatePathPolicyProgramID = "devcrew-path-policy"

type candidatePathPolicyOutcome string

const (
	candidatePathPolicyPassed    candidatePathPolicyOutcome = "passed"
	candidatePathPolicyFailed    candidatePathPolicyOutcome = "failed"
	candidatePathPolicyUnknown   candidatePathPolicyOutcome = "unknown"
	candidatePathPolicyUnchanged candidatePathPolicyOutcome = "unchanged"
)

func (supervisor *candidateSupervisor) inspectCandidatePathPolicy(
	ctx context.Context,
	task domain.Task,
	profile validation.Profile,
	snapshot devgit.CandidateSnapshot,
	latestEvidence *domain.SealedDeliveryEvidence,
	latestJudgment domain.CandidateJudgment,
) (domain.ValidationEvidenceReceipt, candidatePathPolicyOutcome, error) {
	startedAt, err := candidatePathPolicyTime(supervisor.config.Clock)
	if err != nil {
		return domain.ValidationEvidenceReceipt{}, "", err
	}
	view, err := supervisor.config.Git.InspectTaskDiff(ctx, application.TaskDiffRequest{
		TaskHandle: task.Handle, RepositoryID: task.RepositoryID,
		WorktreePath: snapshot.WorktreePath, BaseRevision: task.BaseRevision,
	})
	if err != nil {
		if ctx.Err() != nil {
			return domain.ValidationEvidenceReceipt{}, "", ctx.Err()
		}
		return domain.ValidationEvidenceReceipt{}, "", errors.New("validate task candidate: Git path evidence is unavailable")
	}
	completedAt, err := candidatePathPolicyTime(supervisor.config.Clock)
	if err != nil || completedAt.Before(startedAt) {
		return domain.ValidationEvidenceReceipt{}, "", errors.New("validate task candidate: path evidence time is invalid")
	}
	outcome := classifyCandidatePathPolicy(task, profile, snapshot, view)
	outputHash, err := candidatePathPolicyDigest(profile.PathRules, view, outcome)
	if err != nil {
		return domain.ValidationEvidenceReceipt{}, "", errors.New("validate task candidate: path evidence could not be hashed")
	}
	conclusion := domain.CheckPassed
	switch outcome {
	case candidatePathPolicyPassed:
	case candidatePathPolicyFailed:
		conclusion = domain.CheckFailed
	case candidatePathPolicyUnknown:
		conclusion = domain.CheckUnknown
	default:
		return domain.ValidationEvidenceReceipt{}, "", errors.New("validate task candidate: path evidence outcome is invalid")
	}
	receipt := domain.ValidationEvidenceReceipt{
		CheckID: validation.CandidatePathPolicyCheckID, ProgramID: candidatePathPolicyProgramID,
		HeadRevision: snapshot.HeadRevision, Conclusion: conclusion, Required: true,
		OutputHash: outputHash, StartedAt: startedAt, CompletedAt: completedAt,
	}
	if outcome == candidatePathPolicyUnknown && unchangedCandidatePathPolicyEvidence(task, receipt, latestEvidence, latestJudgment) {
		outcome = candidatePathPolicyUnchanged
	}
	return receipt, outcome, nil
}

func unchangedCandidatePathPolicyEvidence(
	task domain.Task,
	receipt domain.ValidationEvidenceReceipt,
	evidence *domain.SealedDeliveryEvidence,
	judgment domain.CandidateJudgment,
) bool {
	if evidence == nil || judgment.Outcome != domain.CandidateUnknown ||
		judgment.Reason != domain.CandidateValidationUnknown {
		return false
	}
	bundle := evidence.Bundle()
	return bundle.TaskHandle == task.Handle && bundle.RepositoryIdentity == task.RepositoryID &&
		bundle.BaseRevision == task.BaseRevision && bundle.HeadRevision == receipt.HeadRevision &&
		len(bundle.ValidationReceipts) == 1 &&
		bundle.ValidationReceipts[0].CheckID == validation.CandidatePathPolicyCheckID &&
		bundle.ValidationReceipts[0].Conclusion == domain.CheckUnknown &&
		bundle.ValidationReceipts[0].OutputHash == receipt.OutputHash
}

func classifyCandidatePathPolicy(
	task domain.Task,
	profile validation.Profile,
	snapshot devgit.CandidateSnapshot,
	view application.TaskDiffView,
) candidatePathPolicyOutcome {
	if view.TaskHandle != task.Handle || view.RepositoryID != task.RepositoryID ||
		view.BaseRevision != task.BaseRevision || view.HeadRevision != snapshot.HeadRevision ||
		view.FileListTruncated || !taskDiffTotalsMatch(view.Committed, view.CommittedTotals) ||
		!taskDiffTotalsMatch(view.Uncommitted, view.UncommittedTotals) || len(view.Uncommitted) != 0 {
		return candidatePathPolicyUnknown
	}
	for _, change := range view.Committed {
		if !profile.AllowsPath(change.Path) ||
			(change.PreviousPath != "" && !profile.AllowsPath(change.PreviousPath)) {
			return candidatePathPolicyFailed
		}
	}
	return candidatePathPolicyPassed
}

func taskDiffTotalsMatch(changes []application.TaskFileChange, totals application.TaskDiffTotals) bool {
	if totals.Files != len(changes) || totals.Added < 0 || totals.Deleted < 0 || totals.BinaryFiles < 0 {
		return false
	}
	added, deleted, binaryFiles := 0, 0, 0
	for _, change := range changes {
		if change.Binary && change.DetailTruncated || change.DetailTruncated && (change.Added != 0 || change.Deleted != 0) {
			return false
		}
		if change.Added < 0 || change.Deleted < 0 || change.Added > totals.Added-added ||
			change.Deleted > totals.Deleted-deleted {
			return false
		}
		added += change.Added
		deleted += change.Deleted
		if change.Binary {
			binaryFiles++
		}
	}
	return added == totals.Added && deleted == totals.Deleted && binaryFiles == totals.BinaryFiles
}

func candidatePathPolicyDigest(
	rules []validation.PathRule,
	view application.TaskDiffView,
	outcome candidatePathPolicyOutcome,
) (string, error) {
	payload := struct {
		Schema            int                          `json:"schema"`
		Rules             []validation.PathRule        `json:"rules"`
		TaskHandle        string                       `json:"taskHandle"`
		RepositoryID      string                       `json:"repositoryId"`
		BaseRevision      string                       `json:"baseRevision"`
		HeadRevision      string                       `json:"headRevision"`
		Committed         []application.TaskFileChange `json:"committed"`
		Uncommitted       []application.TaskFileChange `json:"uncommitted"`
		CommittedTotals   application.TaskDiffTotals   `json:"committedTotals"`
		UncommittedTotals application.TaskDiffTotals   `json:"uncommittedTotals"`
		Truncated         bool                         `json:"truncated"`
		Outcome           candidatePathPolicyOutcome   `json:"outcome"`
	}{
		Schema: 1, Rules: rules, TaskHandle: view.TaskHandle, RepositoryID: view.RepositoryID,
		BaseRevision: view.BaseRevision, HeadRevision: view.HeadRevision,
		Committed: view.Committed, Uncommitted: view.Uncommitted,
		CommittedTotals: view.CommittedTotals, UncommittedTotals: view.UncommittedTotals,
		Truncated: view.FileListTruncated, Outcome: outcome,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(encoded)), nil
}

func candidatePathPolicyTime(clock application.Clock) (time.Time, error) {
	observedAt := clock()
	if observedAt.IsZero() || observedAt.Location() != time.UTC {
		return time.Time{}, errors.New("validate task candidate: path evidence time is invalid")
	}
	return observedAt, nil
}

func (supervisor *candidateSupervisor) commitCandidatePathPolicyEvidence(
	ctx context.Context,
	task domain.Task,
	profile validation.Profile,
	snapshot devgit.CandidateSnapshot,
	openDecisions int,
	receipt domain.ValidationEvidenceReceipt,
) (domain.Task, domain.CandidateJudgment, error) {
	producedAt := receipt.CompletedAt
	sealed, err := domain.SealDeliveryEvidence(domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: snapshot.HeadRevision,
		WorktreeCleanliness:     candidateCleanliness(snapshot.Cleanliness),
		ValidationReceipts:      []domain.ValidationEvidenceReceipt{receipt},
		UnresolvedDecisionCount: openDecisions, ProducedAt: producedAt,
		ExpiresAt: producedAt.Add(profile.EvidenceTTL).UTC(),
	})
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: path evidence could not be sealed")
	}
	requiredLocal := append(
		[]string{validation.CandidatePathPolicyCheckID}, requiredLocalCheckNames(profile.LocalChecks)...,
	)
	return supervisor.config.Store.CommitCandidateEvidence(
		ctx, task.Handle, sealed, requiredLocal, requiredForgeCheckNames(profile.ForgeChecks), producedAt, nil,
	)
}
