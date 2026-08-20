package application

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func graphInitiative() domain.DevelopmentInitiative {
	return domain.DevelopmentInitiative{
		SchemaVersion:     1,
		Handle:            "initiative-alpha",
		ManagedRunGroupID: "managed-run-group_a",
		State:             domain.InitiativeActive,
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
			{
				FromTaskHandle: "task-backend", ToTaskHandle: "task-frontend",
				Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema,
			},
		},
		IntegrationPolicyID:  "integration-default",
		IntegrationOwnerTask: "task-integration",
		StateVersion:         3,
	}
}

func TestInitiativeGraphProjectsEveryMemberAndEdge(t *testing.T) {
	view := ProjectInitiativeGraph(graphInitiative(), map[string]domain.TaskState{
		"task-backend":     domain.TaskWorking,
		"task-frontend":    domain.TaskReady,
		"task-integration": domain.TaskPrepared,
	}, time.Unix(1_800_000_000, 0).UTC())

	if len(view.Nodes) != 3 || len(view.Edges) != 3 {
		t.Fatalf("nodes = %d edges = %d", len(view.Nodes), len(view.Edges))
	}
	if view.Nodes[0].TaskHandle != "task-backend" || view.Nodes[0].State != domain.TaskWorking {
		t.Fatalf("first node = %+v", view.Nodes[0])
	}
	if view.IntegrationOwnerTask != "task-integration" {
		t.Fatalf("integration owner = %q", view.IntegrationOwnerTask)
	}
}

func TestInitiativeGraphCarriesTheEnrichmentEnvelope(t *testing.T) {
	// §23.3: every live enrichment carries source, confidence, completeness and
	// observation time. A projection without them cannot be told apart from a
	// stale one by whatever consumes it.
	observed := time.Unix(1_800_000_000, 0).UTC()
	view := ProjectInitiativeGraph(graphInitiative(), map[string]domain.TaskState{
		"task-backend": domain.TaskWorking, "task-frontend": domain.TaskReady,
		"task-integration": domain.TaskPrepared,
	}, observed)
	if view.Source != StateSourceStore || view.Confidence == "" ||
		view.Completeness != CompletenessComplete || !view.ObservedAt.Equal(observed) {
		t.Fatalf("envelope = %+v", view)
	}
}

func TestInitiativeGraphReportsPartialWhenAMemberStateIsMissing(t *testing.T) {
	// A node whose state nobody supplied is unknown, and the whole view says so.
	// Rendering it as complete would let a reader treat a gap as a fact.
	view := ProjectInitiativeGraph(graphInitiative(), map[string]domain.TaskState{
		"task-backend": domain.TaskWorking,
	}, time.Unix(1_800_000_000, 0).UTC())
	if view.Completeness != CompletenessPartial {
		t.Fatalf("completeness = %q", view.Completeness)
	}
	for _, node := range view.Nodes {
		if node.TaskHandle != "task-backend" && node.State != domain.TaskUnknown {
			t.Fatalf("missing state rendered as %q", node.State)
		}
	}
}

func TestInitiativeGraphMarksDependencyReadyMembers(t *testing.T) {
	view := ProjectInitiativeGraph(graphInitiative(), map[string]domain.TaskState{
		"task-backend": domain.TaskWorking, "task-frontend": domain.TaskReady,
		"task-integration": domain.TaskPrepared,
	}, time.Unix(1_800_000_000, 0).UTC())
	ready := map[string]bool{}
	for _, node := range view.Nodes {
		ready[node.TaskHandle] = node.DependencyReady
	}
	// Backend has no blocking edge into it. Frontend consumes backend's artifact
	// and integration waits on both, so neither is ready while backend runs.
	if !ready["task-backend"] || ready["task-frontend"] || ready["task-integration"] {
		t.Fatalf("ready = %+v", ready)
	}
}

func TestInitiativeGraphSerializesToStableJSON(t *testing.T) {
	view := ProjectInitiativeGraph(graphInitiative(), map[string]domain.TaskState{
		"task-backend": domain.TaskWorking, "task-frontend": domain.TaskReady,
		"task-integration": domain.TaskPrepared,
	}, time.Unix(1_800_000_000, 0).UTC())
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round InitiativeGraphView
	if err := json.Unmarshal(encoded, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(round.Nodes) != len(view.Nodes) || round.InitiativeHandle != view.InitiativeHandle {
		t.Fatalf("round trip differs: %+v", round)
	}
}

func TestInitiativeGraphCannotMutateTaskState(t *testing.T) {
	// §23.3 forbids a projection mutating state. The states map is the caller's;
	// projecting must not write through it.
	states := map[string]domain.TaskState{"task-backend": domain.TaskWorking}
	ProjectInitiativeGraph(graphInitiative(), states, time.Unix(1_800_000_000, 0).UTC())
	if len(states) != 1 || states["task-backend"] != domain.TaskWorking {
		t.Fatalf("projection mutated the caller's states: %+v", states)
	}
}
