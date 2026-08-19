package application

import (
	"context"
	"time"
)

// DecisionStatus is the closed operator-facing posture of one open decision.
//
// Waiting on the host and waiting on the human are different failures with
// different repairs: the first is a stuck delivery lane, the second is a person
// who has not answered. Collapsing them would make a jammed outbox look like an
// unresponsive liaison.
type DecisionStatus string

const (
	// DecisionAwaitingHost means the question exists but the host has not
	// acknowledged the report that carries it, so nobody has been asked yet.
	DecisionAwaitingHost DecisionStatus = "awaiting_host"
	// DecisionAwaitingHuman means the question reached the host and no
	// resolution has carried its key back.
	DecisionAwaitingHuman DecisionStatus = "awaiting_human"
)

// Valid reports whether the status is one this service can produce.
func (status DecisionStatus) Valid() bool {
	switch status {
	case DecisionAwaitingHost, DecisionAwaitingHuman:
		return true
	default:
		return false
	}
}

// TaskDecision is one open keyed decision as an operator sees it.
//
// Times are pointers where absence is a fact rather than a zero: a question
// nobody has asked has no asking time, and one whose cadence cannot be computed
// has no next airing. Rendering those as an epoch would read as "long overdue".
type TaskDecision struct {
	// StateVersion marks the durable snapshot this row was read in, so a single
	// decision read carries the same read-after-write marker the list does.
	StateVersion int64          `json:"stateVersion"`
	TaskHandle   string         `json:"taskHandle"`
	ExternalKey  string         `json:"externalKey"`
	Status       DecisionStatus `json:"status"`
	Question     string         `json:"question"`
	Detail       string         `json:"detail,omitempty"`
	ReportedAt   time.Time      `json:"reportedAt"`
	AskedAt      *time.Time     `json:"askedAt,omitempty"`
	Airings      int            `json:"airings"`
	LastAiringAt *time.Time     `json:"lastAiringAt,omitempty"`
	NextAiringAt *time.Time     `json:"nextAiringAt,omitempty"`
}

// DecisionList is the bounded operator inventory of open decisions.
type DecisionList struct {
	SchemaVersion int            `json:"schemaVersion"`
	CapturedAt    time.Time      `json:"capturedAt"`
	StateVersion  int64          `json:"stateVersion"`
	Decisions     []TaskDecision `json:"decisions"`
}

// DecisionInventoryStore reads the open decisions an operator may inspect.
type DecisionInventoryStore interface {
	ListTaskDecisions(context.Context, string) ([]TaskDecision, error)
}
