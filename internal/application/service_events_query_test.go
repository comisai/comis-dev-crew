package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

type serviceEventStub struct {
	events  []ServiceEvent
	err     error
	cursors []int64
	limits  []int
}

func (stub *serviceEventStub) ReadServiceEvents(
	_ context.Context,
	afterSequence int64,
	limit int,
	_ string,
) ([]ServiceEvent, error) {
	stub.cursors = append(stub.cursors, afterSequence)
	stub.limits = append(stub.limits, limit)
	return stub.events, stub.err
}

func eventQueryFixture(t *testing.T, stub *serviceEventStub) *Queries {
	t.Helper()
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{stateVersion: 4}, Events: stub,
		Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	return queries
}

// A follower resumes from the cursor it was handed, and the page always states
// the cursor to use next — including when it is empty. A page that omitted the
// next cursor when nothing happened would force the follower to re-read from the
// start and replay the whole log.
func TestReadEvents_HandsBackTheCursorToResumeFrom(t *testing.T) {
	occurred := time.Unix(1_800_000_000, 0).UTC()
	stub := &serviceEventStub{events: []ServiceEvent{
		{Sequence: 7, OccurredAt: occurred, Kind: EventTaskStateChanged, TaskHandle: "task-0001"},
		{Sequence: 9, OccurredAt: occurred, Kind: EventDecisionOpened, TaskHandle: "task-0001", Reason: "schema-choice"},
	}}
	queries := eventQueryFixture(t, stub)

	page, err := queries.ReadEvents(context.Background(), 4, 50, "")
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	if page.SchemaVersion != 1 || len(page.Events) != 2 {
		t.Fatalf("page = %#v", page)
	}
	if page.NextCursor != 9 {
		t.Errorf("next cursor = %d, want the last sequence", page.NextCursor)
	}
	if len(stub.cursors) != 1 || stub.cursors[0] != 4 {
		t.Errorf("store cursors = %v, want the requested cursor", stub.cursors)
	}

	empty := eventQueryFixture(t, &serviceEventStub{})
	quiet, err := empty.ReadEvents(context.Background(), 9, 50, "")
	if err != nil {
		t.Fatalf("ReadEvents(quiet) error = %v", err)
	}
	if quiet.NextCursor != 9 {
		t.Errorf("quiet next cursor = %d, want the cursor unchanged", quiet.NextCursor)
	}
	if quiet.Events == nil {
		t.Error("a quiet page carries a null event list")
	}
}

// The page size is bounded by the service, not by the caller: an unbounded
// request would let one follower ask the service to materialize the whole log.
func TestReadEvents_BoundsThePageTheCallerAsksFor(t *testing.T) {
	stub := &serviceEventStub{}
	queries := eventQueryFixture(t, stub)

	if _, err := queries.ReadEvents(context.Background(), 0, 100_000, ""); err != nil {
		t.Fatalf("ReadEvents(oversize) error = %v", err)
	}
	if len(stub.limits) != 1 || stub.limits[0] > MaximumEventPage {
		t.Errorf("store limits = %v, want it capped at %d", stub.limits, MaximumEventPage)
	}

	if _, err := queries.ReadEvents(context.Background(), 0, 0, ""); err != nil {
		t.Fatalf("ReadEvents(unspecified) error = %v", err)
	}
	if stub.limits[1] <= 0 {
		t.Errorf("an unspecified page size became %d", stub.limits[1])
	}
	if _, err := queries.ReadEvents(context.Background(), -1, 10, ""); err == nil {
		t.Error("ReadEvents(negative cursor) error = nil")
	}
}

// An unreadable stream is a failure, never a quiet one: a follower told "nothing
// happened" would keep redrawing a display it believes is current.
func TestReadEvents_RefusesUnavailableReads(t *testing.T) {
	failing := eventQueryFixture(t, &serviceEventStub{err: errors.New("durable read failed")})
	if _, err := failing.ReadEvents(context.Background(), 0, 10, ""); err == nil {
		t.Error("ReadEvents(store failure) error = nil")
	}

	unconfigured, err := NewQueries(QueryConfig{
		Repository: &queryRepository{stateVersion: 4},
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := unconfigured.ReadEvents(context.Background(), 0, 10, ""); err == nil {
		t.Error("ReadEvents(no event store) error = nil")
	}
}
