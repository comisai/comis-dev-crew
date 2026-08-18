package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type promotionStore struct {
	source    ScoutPromotionSource
	sourceErr error
	link      ScoutPromotionLink
	linkErr   error
	linkCalls int
}

func (store *promotionStore) ReadScoutPromotionSource(
	_ context.Context,
	scoutTaskHandle string,
) (ScoutPromotionSource, error) {
	if store.sourceErr != nil {
		return ScoutPromotionSource{}, store.sourceErr
	}
	source := store.source
	source.ScoutTaskHandle = scoutTaskHandle
	return source, nil
}

func (store *promotionStore) CommitScoutPromotionLink(_ context.Context, link ScoutPromotionLink) error {
	store.linkCalls++
	store.link = link
	return store.linkErr
}

func promotionMutations(t *testing.T, store *mutationStore, promotions ScoutPromotionStore) *Mutations {
	t.Helper()
	mutations := newTestMutations(t, store)
	mutations.promotions = promotions
	return mutations
}

func validPromotion() PromoteScoutCommand {
	return PromoteScoutCommand{
		OperationID: "operation-promote-0001", ServiceInstanceID: "service-instance-0001",
		ScoutTaskHandle:    "task-scout-0001",
		AcceptanceCriteria: []string{"The investigated change is implemented and proven."},
		Constraints:        []string{"Preserve unrelated changes."},
		ValidationProfile:  "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "fixture-worker",
	}
}

// This is the rule the operation exists for. A worker that could turn its own
// scout into a ship task would grant itself the push and pull-request authority
// the scout shape deliberately withholds, and the record of what was
// investigated would be overwritten by the work the investigation produced.
func TestMutations_PromoteScout_MintsANewShipTaskAndLeavesTheScoutAlone(t *testing.T) {
	promotions := &promotionStore{source: ScoutPromotionSource{
		RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		EvidenceDigest: strings.Repeat("f", 64),
	}}
	store := &mutationStore{}
	mutations := promotionMutations(t, store, promotions)

	result, err := mutations.PromoteScout(context.Background(), validPromotion())
	if err != nil {
		t.Fatalf("PromoteScout() error = %v", err)
	}
	if result.Task.Shape != domain.ShapeShip {
		t.Errorf("promoted shape = %q, want ship", result.Task.Shape)
	}
	if result.Task.Handle == "task-scout-0001" {
		t.Fatal("promotion must mint a new task, never rewrite the scout")
	}
	if promotions.linkCalls != 1 {
		t.Fatalf("promotion links recorded = %d, want 1", promotions.linkCalls)
	}
	if promotions.link.ScoutTaskHandle != "task-scout-0001" ||
		promotions.link.ShipTaskHandle != result.Task.Handle {
		t.Errorf("promotion link = %+v", promotions.link)
	}
	// The evidence digest is what makes the link mean something: it names the
	// exact investigation the ship task is justified by.
	if promotions.link.EvidenceDigest != strings.Repeat("f", 64) {
		t.Errorf("link evidence digest = %q", promotions.link.EvidenceDigest)
	}
}

// The ship task must start from the ground the investigation covered. Accepting
// a repository or base revision from the caller would let a promotion point at
// different code than the scout ever looked at, while still carrying the scout's
// evidence as its justification.
func TestMutations_PromoteScout_InheritsTheScoutsGroundNotTheCallers(t *testing.T) {
	promotions := &promotionStore{source: ScoutPromotionSource{
		RepositoryID: "investigated-repo", BaseRevision: strings.Repeat("b", 40),
		EvidenceDigest: strings.Repeat("f", 64),
	}}
	store := &mutationStore{}
	mutations := promotionMutations(t, store, promotions)

	result, err := mutations.PromoteScout(context.Background(), validPromotion())
	if err != nil {
		t.Fatalf("PromoteScout() error = %v", err)
	}
	if result.Task.RepositoryID != "investigated-repo" {
		t.Errorf("promoted repository = %q, want the scout's", result.Task.RepositoryID)
	}
	if result.Task.BaseRevision != strings.Repeat("b", 40) {
		t.Errorf("promoted base revision = %q, want the scout's", result.Task.BaseRevision)
	}
}

// A scout that produced no sealed evidence has investigated nothing, so there is
// nothing for a ship task to be justified by and nothing to preserve.
func TestMutations_PromoteScout_RefusesAScoutWithNoInvestigationToPreserve(t *testing.T) {
	promotions := &promotionStore{sourceErr: ErrNotFound}
	store := &mutationStore{}
	mutations := promotionMutations(t, store, promotions)

	if _, err := mutations.PromoteScout(context.Background(), validPromotion()); err == nil {
		t.Fatal("PromoteScout() with no evidence error = nil, want a refusal")
	}
	if promotions.linkCalls != 0 {
		t.Error("a refused promotion must record no link")
	}
}

// A deployment with no promotion authority refuses rather than minting a ship
// task whose origin nothing records. An unlinked ship task carrying a scout's
// conclusions is indistinguishable from one somebody wrote by hand.
func TestMutations_PromoteScout_RefusesWhenNothingCanRecordTheOrigin(t *testing.T) {
	mutations := newTestMutations(t, &mutationStore{})

	if _, err := mutations.PromoteScout(context.Background(), validPromotion()); err == nil {
		t.Fatal("PromoteScout() without promotion authority error = nil, want a refusal")
	}
}

// A link that cannot be written leaves the promotion incomplete, and the caller
// must hear that rather than a success naming a ship task with no recorded
// origin. Retrying the same operation replays the prepared task and re-attempts
// the link, so the failure is recoverable rather than duplicating work.
func TestMutations_PromoteScout_ReportsAnUnrecordedLinkAsAFailure(t *testing.T) {
	promotions := &promotionStore{
		source: ScoutPromotionSource{
			RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
			EvidenceDigest: strings.Repeat("f", 64),
		},
		linkErr: errors.New("durable link unavailable"),
	}
	store := &mutationStore{}
	mutations := promotionMutations(t, store, promotions)

	if _, err := mutations.PromoteScout(context.Background(), validPromotion()); err == nil {
		t.Fatal("PromoteScout() with an unwritable link error = nil, want a failure")
	}
}

func TestMutations_PromoteScout_RefusesForgedIdentityAndDeadContexts(t *testing.T) {
	promotions := &promotionStore{source: ScoutPromotionSource{
		RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		EvidenceDigest: strings.Repeat("f", 64),
	}}
	mutations := promotionMutations(t, &mutationStore{}, promotions)

	for name, mutate := range map[string]func(*PromoteScoutCommand){
		"no operation":     func(c *PromoteScoutCommand) { c.OperationID = "" },
		"forged operation": func(c *PromoteScoutCommand) { c.OperationID = "../../etc" },
		"no scout":         func(c *PromoteScoutCommand) { c.ScoutTaskHandle = "" },
		"forged scout":     func(c *PromoteScoutCommand) { c.ScoutTaskHandle = "task scout" },
	} {
		command := validPromotion()
		mutate(&command)
		if _, err := mutations.PromoteScout(context.Background(), command); err == nil {
			t.Errorf("%s: expected the promotion to be refused", name)
		}
	}
	if _, err := mutations.PromoteScout(nilPauseContext(), validPromotion()); err == nil {
		t.Error("PromoteScout(nil) error = nil")
	}
}
