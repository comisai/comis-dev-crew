package service

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type remoteLandedEvidence interface {
	ReachableRemoteRefs(context.Context, string, string) ([]string, error)
}

type landedEvidenceComposition struct {
	remotes remoteLandedEvidence
	forge   application.LandedEvidenceGatherer
}

func (composition landedEvidenceComposition) GatherLandedEvidence(
	ctx context.Context,
	request application.LandedEvidenceRequest,
) (application.LandedEvidenceTruth, error) {
	if ctx == nil || composition.remotes == nil || composition.forge == nil {
		return application.LandedEvidenceTruth{}, errors.New("gather landed evidence: sources are unavailable")
	}
	refs, remoteErr := composition.remotes.ReachableRemoteRefs(ctx, request.RepositoryID, request.HeadRevision)
	if remoteErr == nil && len(refs) != 0 {
		return application.LandedEvidenceTruth{
			WorkHead: request.HeadRevision, ReachableFromRemoteRefs: refs,
		}, nil
	}
	truth, forgeErr := composition.forge.GatherLandedEvidence(ctx, request)
	if forgeErr == nil {
		return truth, nil
	}
	return application.LandedEvidenceTruth{}, errors.Join(remoteErr, forgeErr)
}

var _ application.LandedEvidenceGatherer = landedEvidenceComposition{}
