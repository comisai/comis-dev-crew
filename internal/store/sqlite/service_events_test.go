package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Every task state change records exactly one event, in the same transaction
// that changed the state.
//
// Recording it anywhere else would let the two disagree: an event emitted after
// the commit can be lost by a crash, and one emitted before it can describe a
// transition that rolled back. An operator watching the stream would then see a
// state the service never reached, or miss one it did.
func TestServiceEvents_RecordEveryStateChangeInTheSameTransaction(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)

	events, err := store.ReadServiceEvents(context.Background(), 0, 50)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}
	created := len(events)
	if created == 0 {
		t.Fatal("creating a task recorded no event")
	}
	last := events[len(events)-1]
	if last.TaskHandle != task.Handle {
		t.Errorf("event task = %q, want %q", last.TaskHandle, task.Handle)
	}
	if last.State != task.State {
		t.Errorf("event state = %q, want %q", last.State, task.State)
	}
	if last.Sequence <= 0 || last.OccurredAt.IsZero() {
		t.Errorf("event identity = %#v", last)
	}
	if !last.Kind.Valid() {
		t.Errorf("event kind %q is not a declared value", last.Kind)
	}
}

// The cursor is what makes a follower resumable: reading from a sequence returns
// only what happened after it, so a disconnect replays nothing and skips nothing.
func TestServiceEvents_ResumeFromACursorWithoutRepeatingOrSkipping(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	at := task.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, task, "schema-choice", at)

	all, err := store.ReadServiceEvents(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("events = %d, want at least the creation and the decision", len(all))
	}
	for index := 1; index < len(all); index++ {
		if all[index].Sequence <= all[index-1].Sequence {
			t.Fatalf("sequences are not strictly increasing: %#v", all)
		}
	}

	tail, err := store.ReadServiceEvents(context.Background(), all[0].Sequence, 100)
	if err != nil {
		t.Fatalf("ReadServiceEvents(cursor) error = %v", err)
	}
	if len(tail) != len(all)-1 || tail[0].Sequence != all[1].Sequence {
		t.Fatalf("resumed events = %#v, want everything after the first", tail)
	}

	drained, err := store.ReadServiceEvents(context.Background(), all[len(all)-1].Sequence, 100)
	if err != nil {
		t.Fatalf("ReadServiceEvents(drained) error = %v", err)
	}
	if len(drained) != 0 {
		t.Fatalf("a drained cursor returned %#v", drained)
	}
}

// A decision opening is something an operator must act on, so it is its own
// event kind rather than an undifferentiated state change.
func TestServiceEvents_NameTheTransitionsAnOperatorMustActOn(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	at := task.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, task, "schema-choice", at)

	events, err := store.ReadServiceEvents(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}
	var sawDecision bool
	for _, event := range events {
		if event.Kind == application.EventDecisionOpened {
			sawDecision = true
			if event.TaskHandle != task.Handle || event.Reason != "schema-choice" {
				t.Errorf("decision event = %#v", event)
			}
		}
	}
	if !sawDecision {
		t.Fatalf("no decision event among %#v", events)
	}
}

// The stream is content-free by construction: it carries identities, closed
// discriminators and counts, never a question, an objective, a path or a branch.
// A platform-wide view that leaked task content would make the whole stream
// unsafe to show beside other tenants' work.
func TestServiceEvents_CarryNoTaskContent(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)
	at := task.UpdatedAt.Add(time.Minute)
	decision := reportDecision(t, store, task, "schema-choice", at)

	events, err := store.ReadServiceEvents(context.Background(), 0, 100)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}
	for _, event := range events {
		rendered := event.TaskHandle + "|" + string(event.Kind) + "|" + string(event.State) + "|" + event.Reason
		for _, forbidden := range []string{decision.Summary, decision.Details, task.BaseRevision, task.RepositoryID} {
			if forbidden != "" && strings.Contains(rendered, forbidden) {
				t.Errorf("event leaked task content %q: %#v", forbidden, event)
			}
		}
	}
}

// A read that cannot be completed is a failure, never an empty stream: "nothing
// happened" is the one answer a broken read must not be able to give a follower.
func TestServiceEvents_RefuseCanceledCallersAndUnavailableDatabases(t *testing.T) {
	store, _ := attestationFixture(t, domain.ShapeScout)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ReadServiceEvents(canceled, 0, 10); err == nil {
		t.Error("ReadServiceEvents(canceled) error = nil")
	}
	if _, err := store.ReadServiceEvents(missingStoreContext(), 0, 10); err == nil {
		t.Error("ReadServiceEvents(no context) error = nil")
	}
	if _, err := store.ReadServiceEvents(context.Background(), -1, 10); err == nil {
		t.Error("ReadServiceEvents(negative cursor) error = nil")
	}
	if _, err := store.ReadServiceEvents(context.Background(), 0, 0); err == nil {
		t.Error("ReadServiceEvents(zero limit) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.ReadServiceEvents(context.Background(), 0, 10); err == nil {
		t.Error("ReadServiceEvents(closed store) error = nil")
	}
}
