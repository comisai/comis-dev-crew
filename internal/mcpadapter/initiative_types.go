package mcpadapter

import (
	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

// InitiativeInput selects one service-owned initiative.
type InitiativeInput struct {
	InitiativeHandle string `json:"initiativeHandle" jsonschema:"opaque initiative handle"`
}

// BacklogListInput scopes bounded requests without carrying execution authority.
type BacklogListInput struct {
	RepositoryID string                  `json:"repositoryId,omitempty" jsonschema:"optional operator-configured repository identity"`
	Readiness    domain.BacklogReadiness `json:"readiness,omitempty" jsonschema:"optional readiness; use needs_refinement, ready, promoted, or dropped"`
}

type PrepareInitiativeBaseRevision struct {
	RepositoryID string `json:"repositoryId" jsonschema:"operator-configured repository identity"`
	Revision     string `json:"revision" jsonschema:"exact 40-character lowercase hexadecimal Git revision"`
}

type PrepareInitiativePinnedContract struct {
	ArtifactHandle string                      `json:"artifactHandle" jsonschema:"opaque immutable contract artifact handle"`
	Kind           domain.ContractArtifactKind `json:"kind" jsonschema:"closed contract artifact kind"`
	ContentHash    string                      `json:"contentHash" jsonschema:"exact lowercase SHA-256 digest"`
}

type PrepareInitiativeTaskContract struct {
	Shape              domain.TaskShape                  `json:"shape" jsonschema:"task shape; use exactly ship or scout"`
	AcceptanceCriteria []string                          `json:"acceptanceCriteria" jsonschema:"ordered acceptance criteria"`
	Constraints        []string                          `json:"constraints" jsonschema:"ordered task constraints"`
	ConsumedContracts  []PrepareInitiativePinnedContract `json:"consumedContracts,omitempty" jsonschema:"immutable contract pins consumed by this task"`
	ValidationProfile  string                            `json:"validationProfile" jsonschema:"operator-configured validation profile identity"`
	DeliveryMode       domain.DeliveryMode               `json:"deliveryMode" jsonschema:"delivery mode compatible with the task shape"`
	WorkerProfileID    string                            `json:"workerProfileId" jsonschema:"operator-configured worker profile identity"`
}

type PrepareInitiativeTask struct {
	TaskRef  string                        `json:"taskRef" jsonschema:"caller-local task reference used only inside this graph"`
	Contract PrepareInitiativeTaskContract `json:"contract" jsonschema:"immutable member task contract"`
}

type PrepareInitiativeComponent struct {
	ComponentHandle   string                  `json:"componentHandle" jsonschema:"caller-local component handle"`
	RepositoryID      string                  `json:"repositoryId" jsonschema:"operator-configured repository identity"`
	ResponsibilityRef string                  `json:"responsibilityRef" jsonschema:"bounded responsibility reference"`
	Tasks             []PrepareInitiativeTask `json:"tasks" jsonschema:"member tasks owned by this component"`
}

type PrepareInitiativeEdge struct {
	FromTaskRef          string                      `json:"fromTaskRef" jsonschema:"producer caller-local task reference"`
	ToTaskRef            string                      `json:"toTaskRef" jsonschema:"consumer caller-local task reference"`
	Kind                 domain.InitiativeEdgeKind   `json:"kind" jsonschema:"closed dependency kind"`
	RequiredArtifactKind domain.ContractArtifactKind `json:"requiredArtifactKind,omitempty" jsonschema:"artifact kind required by an artifact-consuming edge"`
}

// PrepareInitiativeInput is the complete model-visible graph contract.
type PrepareInitiativeInput struct {
	TitleRef             string                          `json:"titleRef" jsonschema:"bounded private title reference"`
	BaseRevisionSet      []PrepareInitiativeBaseRevision `json:"baseRevisionSet" jsonschema:"one frozen revision per component repository"`
	Components           []PrepareInitiativeComponent    `json:"components" jsonschema:"complete bounded component and task set"`
	Edges                []PrepareInitiativeEdge         `json:"edges" jsonschema:"complete acyclic same-initiative dependency set"`
	ContractArtifacts    []string                        `json:"contractArtifacts" jsonschema:"current immutable contract artifact handles"`
	IntegrationPolicyID  string                          `json:"integrationPolicyId" jsonschema:"operator-configured integration policy identity"`
	IntegrationOwnerTask string                          `json:"integrationOwnerTask,omitempty" jsonschema:"caller-local task reference for the single integration owner"`
}

// PrepareInitiativeOutput omits private host registration metadata.
type PrepareInitiativeOutput struct {
	SchemaVersion    int                      `json:"schemaVersion"`
	OperationID      string                   `json:"operationId"`
	InitiativeHandle string                   `json:"initiativeHandle"`
	State            domain.InitiativeState   `json:"state"`
	StateVersion     int64                    `json:"stateVersion"`
	SideEffect       localapi.SideEffectClass `json:"sideEffect"`
	TaskHandles      []string                 `json:"taskHandles"`
}

func (input PrepareInitiativeInput) local() localapi.PrepareInitiativeInput {
	bases := make([]domain.InitiativeBaseRevision, len(input.BaseRevisionSet))
	for index, base := range input.BaseRevisionSet {
		bases[index] = domain.InitiativeBaseRevision{RepositoryID: base.RepositoryID, Revision: base.Revision}
	}
	components := make([]application.PrepareInitiativeComponent, len(input.Components))
	for componentIndex, component := range input.Components {
		tasks := make([]application.PrepareInitiativeTask, len(component.Tasks))
		for taskIndex, task := range component.Tasks {
			pins := make([]domain.PinnedContract, len(task.Contract.ConsumedContracts))
			for pinIndex, pin := range task.Contract.ConsumedContracts {
				pins[pinIndex] = domain.PinnedContract{
					ArtifactHandle: pin.ArtifactHandle, Kind: pin.Kind, ContentHash: pin.ContentHash,
				}
			}
			tasks[taskIndex] = application.PrepareInitiativeTask{
				TaskRef: task.TaskRef,
				Contract: application.PrepareInitiativeTaskContract{
					Shape:              task.Contract.Shape,
					AcceptanceCriteria: append([]string(nil), task.Contract.AcceptanceCriteria...),
					Constraints:        append([]string(nil), task.Contract.Constraints...),
					ConsumedContracts:  pins, ValidationProfile: task.Contract.ValidationProfile,
					DeliveryMode: task.Contract.DeliveryMode, WorkerProfileID: task.Contract.WorkerProfileID,
				},
			}
		}
		components[componentIndex] = application.PrepareInitiativeComponent{
			ComponentHandle: component.ComponentHandle, RepositoryID: component.RepositoryID,
			ResponsibilityRef: component.ResponsibilityRef, Tasks: tasks,
		}
	}
	edges := make([]application.PrepareInitiativeEdge, len(input.Edges))
	for index, edge := range input.Edges {
		edges[index] = application.PrepareInitiativeEdge{
			FromTaskRef: edge.FromTaskRef, ToTaskRef: edge.ToTaskRef,
			Kind: edge.Kind, RequiredArtifactKind: edge.RequiredArtifactKind,
		}
	}
	return localapi.PrepareInitiativeInput{
		TitleRef: input.TitleRef, BaseRevisionSet: bases, Components: components, Edges: edges,
		ContractArtifacts:   append([]string(nil), input.ContractArtifacts...),
		IntegrationPolicyID: input.IntegrationPolicyID, IntegrationOwnerTask: input.IntegrationOwnerTask,
	}
}
