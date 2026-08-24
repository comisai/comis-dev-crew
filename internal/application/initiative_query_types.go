package application

import (
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	// MaximumInitiativePage bounds one durable initiative page.
	MaximumInitiativePage = 16
	defaultInitiativePage = MaximumInitiativePage
	// MaximumBacklogPage bounds one durable backlog page.
	MaximumBacklogPage = 16
	defaultBacklogPage = MaximumBacklogPage
)

// InitiativeNextAction is a closed, non-executable initiative action.
type InitiativeNextAction string

const (
	InitiativeActionInspect InitiativeNextAction = "inspect_initiative"
	InitiativeActionPause   InitiativeNextAction = "pause_initiative"
	InitiativeActionResume  InitiativeNextAction = "resume_initiative"
	InitiativeActionCancel  InitiativeNextAction = "cancel_initiative"
	InitiativeActionNone    InitiativeNextAction = "none"
)

// InitiativeSummary is one bounded initiative-list row.
type InitiativeSummary struct {
	InitiativeHandle string                 `json:"initiativeHandle"`
	TitleRef         string                 `json:"titleRef"`
	State            domain.InitiativeState `json:"state"`
	StateVersion     int64                  `json:"stateVersion"`
	ComponentCount   int                    `json:"componentCount"`
	TaskCount        int                    `json:"taskCount"`
	UpdatedAt        time.Time              `json:"updatedAt"`
}

// InitiativeFilter scopes and pages durable initiative summaries.
type InitiativeFilter struct {
	State       domain.InitiativeState `json:"state,omitempty"`
	AfterHandle string                 `json:"afterHandle,omitempty"`
	Limit       int                    `json:"limit,omitempty"`
}

// InitiativeList is a versioned deterministic initiative projection.
type InitiativeList struct {
	SchemaVersion int                 `json:"schemaVersion"`
	CapturedAtMs  int64               `json:"capturedAtMs"`
	StateVersion  int64               `json:"stateVersion"`
	NextCursor    string              `json:"nextCursor,omitempty"`
	Initiatives   []InitiativeSummary `json:"initiatives"`
}

// InitiativeDetail joins one durable record to its complete graph and safe actions.
type InitiativeDetail struct {
	SchemaVersion   int                          `json:"schemaVersion"`
	CapturedAtMs    int64                        `json:"capturedAtMs"`
	StateVersion    int64                        `json:"stateVersion"`
	Initiative      domain.DevelopmentInitiative `json:"initiative"`
	Graph           InitiativeGraphView          `json:"graph"`
	ReasonCode      string                       `json:"reasonCode"`
	Explanation     string                       `json:"explanation"`
	NextSafeActions []InitiativeNextAction       `json:"nextSafeActions"`
}

// BacklogFilter scopes and pages the durable backlog without granting work authority.
type BacklogFilter struct {
	RepositoryID string                  `json:"repositoryId,omitempty"`
	Readiness    domain.BacklogReadiness `json:"readiness,omitempty"`
	AfterHandle  string                  `json:"afterHandle,omitempty"`
	Limit        int                     `json:"limit,omitempty"`
}

// BacklogList is the versioned bounded-request projection.
type BacklogList struct {
	SchemaVersion int                  `json:"schemaVersion"`
	CapturedAtMs  int64                `json:"capturedAtMs"`
	StateVersion  int64                `json:"stateVersion"`
	NextCursor    string               `json:"nextCursor,omitempty"`
	Items         []domain.BacklogItem `json:"items"`
}
