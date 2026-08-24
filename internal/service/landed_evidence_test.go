package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

type remoteLandedEvidenceStub struct {
	refs []string
	err  error
}

func (stub remoteLandedEvidenceStub) ReachableRemoteRefs(context.Context, string, string) ([]string, error) {
	return stub.refs, stub.err
}

type forgeLandedEvidenceStub struct {
	truth  application.LandedEvidenceTruth
	err    error
	called *bool
}

func (stub forgeLandedEvidenceStub) GatherLandedEvidence(
	context.Context,
	application.LandedEvidenceRequest,
) (application.LandedEvidenceTruth, error) {
	*stub.called = true
	return stub.truth, stub.err
}

func TestLandedEvidenceCompositionAcceptsIndependentProofRoutes(t *testing.T) {
	request := application.LandedEvidenceRequest{
		RepositoryID: "product-api", Branch: "devcrew/task", HeadRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	t.Run("remote tracking ref", func(t *testing.T) {
		called := false
		composition := landedEvidenceComposition{
			remotes: remoteLandedEvidenceStub{refs: []string{"fork/feature"}},
			forge:   forgeLandedEvidenceStub{err: errors.New("forge unavailable"), called: &called},
		}
		truth, err := composition.GatherLandedEvidence(context.Background(), request)
		if err != nil || truth.WorkHead != request.HeadRevision ||
			!reflect.DeepEqual(truth.ReachableFromRemoteRefs, []string{"fork/feature"}) || called {
			t.Fatalf("GatherLandedEvidence(remote) = %#v, %v; forge called=%t", truth, err, called)
		}
	})
	t.Run("forge", func(t *testing.T) {
		called := false
		want := application.LandedEvidenceTruth{
			WorkHead: request.HeadRevision, Available: true, DefaultBranchContainsHead: true,
		}
		composition := landedEvidenceComposition{
			remotes: remoteLandedEvidenceStub{},
			forge:   forgeLandedEvidenceStub{truth: want, called: &called},
		}
		truth, err := composition.GatherLandedEvidence(context.Background(), request)
		if err != nil || !reflect.DeepEqual(truth, want) || !called {
			t.Fatalf("GatherLandedEvidence(forge) = %#v, %v; forge called=%t", truth, err, called)
		}
	})
}
