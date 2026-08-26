package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func initiativeFixture() domain.DevelopmentInitiative {
	now := time.Unix(1_800_000_000, 0).UTC()
	return domain.DevelopmentInitiative{
		SchemaVersion:     1,
		Handle:            "initiative-alpha",
		ManagedRunGroupID: "managed-run-group_a",
		TitleRef:          "title-alpha",
		State:             domain.InitiativePreparing,
		BaseRevisionSet: []domain.InitiativeBaseRevision{
			{RepositoryID: "repo-primary", Revision: "0123456789abcdef0123456789abcdef01234567"},
		},
		Components: []domain.InitiativeComponent{
			{ComponentHandle: "component-backend", RepositoryID: "repo-primary", TaskHandles: []string{"task-backend"}},
			{ComponentHandle: "component-frontend", RepositoryID: "repo-primary", TaskHandles: []string{"task-frontend"}},
			{ComponentHandle: "component-integration", RepositoryID: "repo-primary", TaskHandles: []string{"task-integration"}},
		},
		Edges: []domain.InitiativeEdge{
			{FromTaskHandle: "task-backend", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
			{FromTaskHandle: "task-frontend", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
		},
		IntegrationPolicyID:  "integration-default",
		IntegrationOwnerTask: "task-integration",
		StateVersion:         1,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func TestInitiativeAcceptsAcyclicSameInitiativeGraph(t *testing.T) {
	if err := initiativeFixture().Validate(); err != nil {
		t.Fatalf("valid initiative rejected: %v", err)
	}
}

func TestInitiativeTitleReferenceIsBoundedSafeText(t *testing.T) {
	for _, title := range []string{"", strings.Repeat("t", 257), "unsafe\ntitle"} {
		initiative := initiativeFixture()
		initiative.TitleRef = title
		if err := initiative.Validate(); err == nil {
			t.Fatalf("initiative title %q accepted", title)
		}
	}
}

func TestPreparingInitiativeCanWaitForItsHostGroupBinding(t *testing.T) {
	initiative := initiativeFixture()
	initiative.ManagedRunGroupID = ""
	if err := initiative.Validate(); err != nil {
		t.Fatalf("unbound preparing initiative rejected: %v", err)
	}
	initiative.State = domain.InitiativeUnknown
	if err := initiative.Validate(); err != nil {
		t.Fatalf("unbound unknown initiative rejected: %v", err)
	}
	initiative.State = domain.InitiativeActive
	if err := initiative.Validate(); err == nil {
		t.Fatal("active initiative without a host group binding accepted")
	}
}

func TestInitiativeRejectsCycle(t *testing.T) {
	initiative := initiativeFixture()
	// A cycle has no schedulable start, so every member would wait on another
	// member forever. It must be refused at the record, not discovered by a
	// scheduler that stalls.
	initiative.Edges = append(initiative.Edges, domain.InitiativeEdge{
		FromTaskHandle: "task-integration", ToTaskHandle: "task-backend", Kind: domain.EdgeBlocksStart,
	})
	if err := initiative.Validate(); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestInitiativeRejectsValidationCycle(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = []domain.InitiativeEdge{
		{FromTaskHandle: "task-backend", ToTaskHandle: "task-frontend", Kind: domain.EdgeBlocksValidation},
		{FromTaskHandle: "task-frontend", ToTaskHandle: "task-backend", Kind: domain.EdgeBlocksValidation},
	}
	if err := initiative.Validate(); err == nil {
		t.Fatal("validation cycle accepted")
	}
}

func TestInitiativeRejectsSelfEdge(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = append(initiative.Edges, domain.InitiativeEdge{
		FromTaskHandle: "task-backend", ToTaskHandle: "task-backend", Kind: domain.EdgeBlocksStart,
	})
	if err := initiative.Validate(); err == nil {
		t.Fatal("self edge accepted")
	}
}

func TestInitiativeRejectsEdgeToNonMember(t *testing.T) {
	initiative := initiativeFixture()
	// An edge naming a task this initiative does not contain is how a
	// cross-initiative dependency would sneak in.
	initiative.Edges = append(initiative.Edges, domain.InitiativeEdge{
		FromTaskHandle: "task-backend", ToTaskHandle: "task-elsewhere", Kind: domain.EdgeBlocksStart,
	})
	if err := initiative.Validate(); err == nil {
		t.Fatal("edge to a non-member accepted")
	}
}

func TestInitiativeRejectsDuplicateEdge(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = append(initiative.Edges, initiative.Edges[0])
	if err := initiative.Validate(); err == nil {
		t.Fatal("duplicate edge accepted")
	}
}

func TestInitiativeRejectsTaskInTwoComponents(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Components[1].TaskHandles = []string{"task-backend"}
	if err := initiative.Validate(); err == nil {
		t.Fatal("task shared between components accepted")
	}
}

func TestInitiativeRequiresIntegrationOwnerToBeAMember(t *testing.T) {
	initiative := initiativeFixture()
	initiative.IntegrationOwnerTask = "task-elsewhere"
	if err := initiative.Validate(); err == nil {
		t.Fatal("integration owner outside the initiative accepted")
	}
}

func TestInitiativeRequiresOneBaseRevisionPerRepository(t *testing.T) {
	initiative := initiativeFixture()
	// Two revisions for one repository would let two components call different
	// bases "the" base and both claim their evidence is current.
	initiative.BaseRevisionSet = append(initiative.BaseRevisionSet, domain.InitiativeBaseRevision{
		RepositoryID: "repo-primary", Revision: "89abcdef0123456789abcdef0123456789abcdef",
	})
	if err := initiative.Validate(); err == nil {
		t.Fatal("duplicate repository base revision accepted")
	}
}

func TestInitiativeRequiresABaseRevisionForEveryComponentRepository(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Components[0].RepositoryID = "repo-secondary"
	if err := initiative.Validate(); err == nil {
		t.Fatal("component repository without a frozen base accepted")
	}
}

func TestInitiativeRejectsAConsumesArtifactEdgeWithoutAnArtifactKind(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges = append(initiative.Edges, domain.InitiativeEdge{
		FromTaskHandle: "task-backend", ToTaskHandle: "task-frontend", Kind: domain.EdgeConsumesArtifact,
	})
	if err := initiative.Validate(); err == nil {
		t.Fatal("artifact edge without a required artifact kind accepted")
	}
}

func TestInitiativeRejectsARequiredArtifactKindOnANonArtifactEdge(t *testing.T) {
	initiative := initiativeFixture()
	initiative.Edges[0].RequiredArtifactKind = domain.ArtifactAPISchema
	if err := initiative.Validate(); err == nil {
		t.Fatal("required artifact kind on a non-artifact edge accepted")
	}
}

func TestInitiativeReadyTasksAreThoseWithNoUnsatisfiedBlockingEdge(t *testing.T) {
	initiative := initiativeFixture()
	ready := initiative.DependencyReadyTasks(map[string]bool{})
	// Both component lanes start together; only integration waits. Membership in
	// one repository is not a reason to serialize, because each task gets its
	// own worktree.
	if len(ready) != 2 || ready[0] != "task-backend" || ready[1] != "task-frontend" {
		t.Fatalf("ready = %v", ready)
	}
	ready = initiative.DependencyReadyTasks(map[string]bool{"task-backend": true})
	if len(ready) != 1 || ready[0] != "task-frontend" {
		t.Fatalf("ready after one completion = %v", ready)
	}
	ready = initiative.DependencyReadyTasks(map[string]bool{"task-backend": true, "task-frontend": true})
	if len(ready) != 1 || ready[0] != "task-integration" {
		t.Fatalf("ready after both completions = %v", ready)
	}
}
