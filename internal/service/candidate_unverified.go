package service

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func candidateRequiresUnverifiedEvidence(task domain.Task, snapshot devgit.CandidateSnapshot) bool {
	return snapshot.Cleanliness != devgit.CandidateClean || snapshot.HeadRevision == task.BaseRevision
}

func (supervisor *candidateSupervisor) commitUnverifiedCandidate(
	ctx context.Context,
	task domain.Task,
	profile validation.Profile,
	snapshot devgit.CandidateSnapshot,
	openDecisions int,
) (domain.Task, domain.CandidateJudgment, error) {
	producedAt := supervisor.config.Clock()
	if producedAt.IsZero() || producedAt.Location() != time.UTC {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: evidence time is invalid")
	}
	sealed, err := domain.SealDeliveryEvidence(domain.DeliveryEvidenceBundle{
		SchemaVersion: 1, TaskHandle: task.Handle, RepositoryIdentity: task.RepositoryID,
		BaseRevision: task.BaseRevision, HeadRevision: snapshot.HeadRevision,
		WorktreeCleanliness:     candidateCleanliness(snapshot.Cleanliness),
		UnresolvedDecisionCount: openDecisions, ProducedAt: producedAt,
		ExpiresAt: producedAt.Add(profile.EvidenceTTL).UTC(),
	})
	if err != nil {
		return domain.Task{}, domain.CandidateJudgment{}, errors.New("validate task candidate: unverified evidence could not be sealed")
	}
	return supervisor.config.Store.CommitCandidateEvidence(
		ctx, task.Handle, sealed, requiredLocalCheckNames(profile.LocalChecks),
		requiredForgeCheckNames(profile.ForgeChecks), producedAt, nil,
	)
}

func requiredLocalCheckNames(checks []validation.LocalCheck) []string {
	required := make([]string, 0, len(checks))
	for _, check := range checks {
		if check.Required {
			required = append(required, check.ID)
		}
	}
	return required
}
