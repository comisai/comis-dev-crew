package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ServiceEventKind is the closed set of transitions an operator may need to act
// on.
//
// It is a closed discriminator rather than free text because the stream is a
// machine surface first: a watcher decides what to redraw from the kind, and an
// open vocabulary would make that decision unstable across releases.
type ServiceEventKind string

const (
	// EventTaskStateChanged is one durable task state transition.
	EventTaskStateChanged ServiceEventKind = "task_state_changed"
	// EventDecisionOpened is a keyed question that now awaits a human.
	EventDecisionOpened ServiceEventKind = "decision_opened"
	// EventDecisionResolved is a keyed question that no longer does.
	EventDecisionResolved ServiceEventKind = "decision_resolved"
)

// Valid reports whether the kind is one this service can produce.
func (kind ServiceEventKind) Valid() bool {
	switch kind {
	case EventTaskStateChanged, EventDecisionOpened, EventDecisionResolved:
		return true
	default:
		return false
	}
}

// ServiceEvent is one content-free record of something that happened.
//
// It carries identities, closed discriminators, a version and a time — never a
// question, an objective, a path, a branch or a report body. The stream is
// readable beside unrelated work, so anything that could identify what a task is
// about belongs in the authenticated per-task reads instead.
//
// Reason is a closed code or an opaque key (a decision's external key), never
// prose.
type ServiceEvent struct {
	Sequence     int64            `json:"sequence"`
	OccurredAt   time.Time        `json:"occurredAt"`
	Kind         ServiceEventKind `json:"kind"`
	TaskHandle   string           `json:"taskHandle,omitempty"`
	State        domain.TaskState `json:"state,omitempty"`
	Reason       string           `json:"reason,omitempty"`
	StateVersion int64            `json:"stateVersion,omitempty"`
}

// ServiceEventStore reads the durable event log from a cursor.
type ServiceEventStore interface {
	ReadServiceEvents(context.Context, int64, int) ([]ServiceEvent, error)
}
