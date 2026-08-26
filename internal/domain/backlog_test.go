package domain_test

import (
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func backlogFixture() domain.BacklogItem {
	now := time.Unix(1_800_000_000, 0).UTC()
	return domain.BacklogItem{
		SchemaVersion:         1,
		Handle:                "backlog-0001",
		RepositoryID:          "repo-primary",
		Shape:                 domain.ShapeShip,
		RequestedOutcome:      "Replace the deprecated pagination parameters.",
		Priority:              domain.BacklogPriorityNormal,
		Readiness:             domain.BacklogReady,
		SourceConversationRef: "cv_" + "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func TestBacklogItemAcceptsABoundedRequest(t *testing.T) {
	if err := backlogFixture().Validate(); err != nil {
		t.Fatalf("valid backlog item rejected: %v", err)
	}
}

func TestBacklogItemCarriesNoRunAuthority(t *testing.T) {
	// §19.4 is explicit: a backlog record carries no terminal, credential or
	// delivery authority by itself. The record has nowhere to PUT such a field,
	// which is a stronger guarantee than validating one away, so this asserts
	// the shape rather than a rejection.
	item := backlogFixture()
	for _, field := range domain.BacklogItemFieldNames(item) {
		switch field {
		case "ManagedRunID", "WorkspaceLeaseID", "ExecutionAttachmentID",
			"DeliveryMode", "CredentialRef", "TerminalSessionID":
			t.Fatalf("backlog record carries run authority field %q", field)
		}
	}
}

func TestBacklogItemRejectsAnUnboundedOutcome(t *testing.T) {
	item := backlogFixture()
	item.RequestedOutcome = ""
	if err := item.Validate(); err == nil {
		t.Fatal("empty requested outcome accepted")
	}
	item.RequestedOutcome = string(make([]byte, 8193))
	if err := item.Validate(); err == nil {
		t.Fatal("unbounded requested outcome accepted")
	}
}

func TestBacklogItemRejectsADependencyOnItself(t *testing.T) {
	item := backlogFixture()
	item.DependsOn = []string{item.Handle}
	if err := item.Validate(); err == nil {
		t.Fatal("self dependency accepted")
	}
}

func TestBacklogItemRejectsDuplicateDependencies(t *testing.T) {
	item := backlogFixture()
	item.DependsOn = []string{"backlog-0002", "backlog-0002"}
	if err := item.Validate(); err == nil {
		t.Fatal("duplicate dependency accepted")
	}
}

func TestOnlyAReadyItemWithSatisfiedDependenciesMayBePromoted(t *testing.T) {
	item := backlogFixture()
	item.DependsOn = []string{"backlog-0002"}

	if err := item.CheckPromotable(map[string]bool{}); err == nil {
		t.Fatal("item with an unsatisfied dependency was promotable")
	}
	if err := item.CheckPromotable(map[string]bool{"backlog-0002": true}); err != nil {
		t.Fatalf("ready item with satisfied dependencies refused: %v", err)
	}

	item.Readiness = domain.BacklogNeedsRefinement
	if err := item.CheckPromotable(map[string]bool{"backlog-0002": true}); err == nil {
		t.Fatal("an unrefined item was promotable")
	}
}

func TestAPromotedItemCannotBePromotedTwice(t *testing.T) {
	// Promotion prepares a real managed run. Promoting twice would bind two runs
	// to one request and leave the second orphaned.
	item := backlogFixture()
	item.Readiness = domain.BacklogPromoted
	if err := item.CheckPromotable(map[string]bool{}); err == nil {
		t.Fatal("an already promoted item was promotable again")
	}
}

func TestBacklogShapeAndPriorityAreClosedSets(t *testing.T) {
	item := backlogFixture()
	item.Shape = "archaeologist"
	if err := item.Validate(); err == nil {
		t.Fatal("unknown shape accepted")
	}
	item = backlogFixture()
	item.Priority = "whenever"
	if err := item.Validate(); err == nil {
		t.Fatal("unknown priority accepted")
	}
}
