package application

import (
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// InitiativeGraphNode is one member as the projection sees it.
type InitiativeGraphNode struct {
	TaskHandle       string           `json:"taskHandle"`
	ComponentHandle  string           `json:"componentHandle"`
	RepositoryID     string           `json:"repositoryId"`
	State            domain.TaskState `json:"state"`
	DependencyReady  bool             `json:"dependencyReady"`
	IntegrationOwner bool             `json:"integrationOwner"`
}

// InitiativeGraphEdge is one dependency, carrying why it waits.
type InitiativeGraphEdge struct {
	From                 string                      `json:"from"`
	To                   string                      `json:"to"`
	Kind                 domain.InitiativeEdgeKind   `json:"kind"`
	RequiredArtifactKind domain.ContractArtifactKind `json:"requiredArtifactKind,omitempty"`
}

// InitiativeGraphView is the §23.3 projection of one initiative.
//
// It is a read. The envelope — source, confidence, completeness and observation
// time — travels with it because a consumer that cannot tell a complete view
// from a partial one will read a gap as a fact.
type InitiativeGraphView struct {
	InitiativeHandle     string                 `json:"initiativeHandle"`
	ManagedRunGroupID    string                 `json:"managedRunGroupId"`
	State                domain.InitiativeState `json:"state"`
	StateVersion         int64                  `json:"stateVersion"`
	IntegrationOwnerTask string                 `json:"integrationOwnerTask,omitempty"`
	Nodes                []InitiativeGraphNode  `json:"nodes"`
	Edges                []InitiativeGraphEdge  `json:"edges"`
	Source               StateSource            `json:"source"`
	Confidence           Confidence             `json:"confidence"`
	Completeness         Completeness           `json:"completeness"`
	ObservedAt           time.Time              `json:"observedAt"`
}

// ProjectInitiativeGraph renders one initiative as the detailed fleet
// projection.
//
// The caller's state map is only read. A projection that wrote through it would
// be mutating task state from a view, which §23.3 forbids outright.
//
// A member whose state nobody supplied is projected unknown and drops the whole
// view to partial, rather than being quietly omitted — an absent node reads as
// an initiative with fewer members, which is a different and wrong claim.
func ProjectInitiativeGraph(
	initiative domain.DevelopmentInitiative,
	states map[string]domain.TaskState,
	observedAt time.Time,
) InitiativeGraphView {
	view := InitiativeGraphView{
		InitiativeHandle:     initiative.Handle,
		ManagedRunGroupID:    initiative.ManagedRunGroupID,
		State:                initiative.State,
		StateVersion:         initiative.StateVersion,
		IntegrationOwnerTask: initiative.IntegrationOwnerTask,
		Source:               StateSourceStore,
		Confidence:           ConfidenceVerified,
		Completeness:         CompletenessComplete,
		ObservedAt:           observedAt,
	}

	// Dependency readiness is derived from the states the caller supplied, so a
	// member whose state is unknown cannot satisfy anything downstream.
	satisfied := make(map[string]bool, len(states))
	for handle, state := range states {
		satisfied[handle] = state == domain.TaskDelivered || state == domain.TaskCleaned
	}
	ready := make(map[string]bool)
	for _, handle := range initiative.DependencyReadyTasks(satisfied) {
		ready[handle] = true
	}

	for _, component := range initiative.Components {
		for _, handle := range component.TaskHandles {
			state, known := states[handle]
			if !known {
				state = domain.TaskUnknown
				view.Completeness = CompletenessPartial
				view.Confidence = ConfidenceUnknown
			}
			view.Nodes = append(view.Nodes, InitiativeGraphNode{
				TaskHandle:       handle,
				ComponentHandle:  component.ComponentHandle,
				RepositoryID:     component.RepositoryID,
				State:            state,
				DependencyReady:  ready[handle],
				IntegrationOwner: handle == initiative.IntegrationOwnerTask,
			})
		}
	}
	sort.Slice(view.Nodes, func(left, right int) bool {
		return view.Nodes[left].TaskHandle < view.Nodes[right].TaskHandle
	})

	for _, edge := range initiative.Edges {
		view.Edges = append(view.Edges, InitiativeGraphEdge{
			From: edge.FromTaskHandle, To: edge.ToTaskHandle,
			Kind: edge.Kind, RequiredArtifactKind: edge.RequiredArtifactKind,
		})
	}
	sort.Slice(view.Edges, func(left, right int) bool {
		if view.Edges[left].From != view.Edges[right].From {
			return view.Edges[left].From < view.Edges[right].From
		}
		return view.Edges[left].To < view.Edges[right].To
	})
	return view
}
