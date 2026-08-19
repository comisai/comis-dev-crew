package application

import (
	"context"
	"errors"
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

	// EventDecisionAnswered records that the human replied. It is deliberately
	// distinct from a resolution: the answer has landed, but the worker has not
	// yet applied it, and an operator watching the fleet needs to see both.
	EventDecisionAnswered ServiceEventKind = "decision_answered"
)

// Valid reports whether the kind is one this service can produce.
func (kind ServiceEventKind) Valid() bool {
	switch kind {
	case EventTaskStateChanged, EventDecisionOpened, EventDecisionResolved, EventDecisionAnswered:
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
	ReadServiceEvents(context.Context, int64, int, string) ([]ServiceEvent, error)
}

// MaximumEventPage bounds one event page. A caller may ask for less; asking for
// more is capped rather than refused, so a follower written against a later
// release still makes progress.
const MaximumEventPage = 200

// defaultEventPage is used when a caller states no preference.
const defaultEventPage = 100

// EventPage is one bounded, resumable slice of the service event stream.
type EventPage struct {
	SchemaVersion int            `json:"schemaVersion"`
	CapturedAt    time.Time      `json:"capturedAt"`
	NextCursor    int64          `json:"nextCursor"`
	Events        []ServiceEvent `json:"events"`
}

// ReadEvents returns the events after a cursor and the cursor to resume from.
//
// The next cursor is returned even when the page is empty, so a quiet interval
// costs a follower nothing: without it, a follower that saw no events would have
// to re-read from its last known sequence and could never distinguish "nothing
// happened" from "I lost my place".
func (queries *Queries) ReadEvents(ctx context.Context, afterSequence int64, limit int, taskHandle string) (EventPage, error) {
	if afterSequence < 0 {
		return EventPage{}, invalidReferenceFailure("event cursor", errors.New("cursor must not be negative"))
	}
	if taskHandle != "" && domain.ValidateTaskHandle(taskHandle) != nil {
		return EventPage{}, invalidReferenceFailure("event task scope", errors.New("task handle is invalid"))
	}
	if queries.events == nil {
		return EventPage{}, translateReadError(nil, "event stream")
	}
	if limit <= 0 {
		limit = defaultEventPage
	}
	if limit > MaximumEventPage {
		limit = MaximumEventPage
	}
	events, err := queries.events.ReadServiceEvents(ctx, afterSequence, limit, taskHandle)
	if err != nil {
		return EventPage{}, translateReadError(err, "event stream")
	}
	if events == nil {
		events = []ServiceEvent{}
	}
	next := afterSequence
	if len(events) != 0 {
		next = events[len(events)-1].Sequence
	}
	return EventPage{
		SchemaVersion: 1, CapturedAt: queries.now(), NextCursor: next, Events: events,
	}, nil
}
