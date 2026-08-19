package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type decisionInventoryStub struct {
	decisions []TaskDecision
	scopes    []string
	err       error
}

func (stub *decisionInventoryStub) ListTaskDecisions(
	_ context.Context,
	taskHandle string,
) ([]TaskDecision, error) {
	stub.scopes = append(stub.scopes, taskHandle)
	return stub.decisions, stub.err
}

func decisionQueryFixture(t *testing.T, stub *decisionInventoryStub) *Queries {
	t.Helper()
	queries, err := NewQueries(QueryConfig{
		Repository: &queryRepository{}, Decisions: stub,
		DecisionSurfacing: DecisionSurfacingPolicy{Initial: 30 * time.Minute, Maximum: 4 * time.Hour},
		Clock:             func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	return queries
}

func askedDecision(airings int, lastAiring time.Time) TaskDecision {
	return TaskDecision{
		TaskHandle: "task-decision-0001", ExternalKey: "schema-choice",
		Status: DecisionAwaitingHuman, Question: "which migration order applies",
		ReportedAt: lastAiring, AskedAt: &lastAiring, Airings: airings, LastAiringAt: &lastAiring,
	}
}

// An operator asking "what is waiting on me" needs to know when it will come
// back, not only that it will. The cadence is derived once, here, so the CLI
// cannot compute a schedule that disagrees with the supervisor's.
func TestListDecisions_StatesWhenAnUnansweredQuestionReturns(t *testing.T) {
	aired := time.Unix(1_800_000_000, 0).UTC()
	stub := &decisionInventoryStub{decisions: []TaskDecision{askedDecision(1, aired)}}
	queries := decisionQueryFixture(t, stub)

	list, err := queries.ListDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if list.SchemaVersion != 1 || len(list.Decisions) != 1 {
		t.Fatalf("decision list = %#v", list)
	}
	next := list.Decisions[0].NextAiringAt
	if next == nil {
		t.Fatal("an asked decision reports no next airing")
	}
	if want := aired.Add(30 * time.Minute); !next.Equal(want) {
		t.Errorf("next airing = %s, want %s", next, want)
	}
	if len(stub.scopes) != 1 || stub.scopes[0] != "" {
		t.Errorf("store scopes = %v, want one fleet-wide read", stub.scopes)
	}
}

// A question the host has not been told about has no cadence yet: the outbox
// owes the first airing, not the supervisor. Publishing a schedule for it would
// promise a repeat of something nobody has asked.
func TestListDecisions_PromisesNoScheduleBeforeTheFirstAiring(t *testing.T) {
	unasked := TaskDecision{
		TaskHandle: "task-decision-0001", ExternalKey: "schema-choice",
		Status: DecisionAwaitingHost, Question: "which migration order applies",
		ReportedAt: time.Unix(1_800_000_000, 0).UTC(),
	}
	queries := decisionQueryFixture(t, &decisionInventoryStub{decisions: []TaskDecision{unasked}})

	list, err := queries.ListDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	if len(list.Decisions) != 1 || list.Decisions[0].NextAiringAt != nil {
		t.Fatalf("unasked decision = %#v, want no scheduled airing", list.Decisions)
	}
}

// Showing one decision is a scoped read, not a client-side filter over the whole
// fleet: an operator naming a task must not be handed another task's questions.
func TestShowDecision_ReadsExactlyTheNamedQuestion(t *testing.T) {
	aired := time.Unix(1_800_000_000, 0).UTC()
	stub := &decisionInventoryStub{decisions: []TaskDecision{askedDecision(1, aired)}}
	queries := decisionQueryFixture(t, stub)

	decision, err := queries.ShowDecision(context.Background(), "task-decision-0001", "schema-choice")
	if err != nil {
		t.Fatalf("ShowDecision() error = %v", err)
	}
	if decision.ExternalKey != "schema-choice" || decision.NextAiringAt == nil {
		t.Fatalf("decision = %#v", decision)
	}
	if len(stub.scopes) != 1 || stub.scopes[0] != "task-decision-0001" {
		t.Errorf("store scopes = %v, want the named task", stub.scopes)
	}

	if _, err := queries.ShowDecision(context.Background(), "task-decision-0001", "other-key"); err == nil {
		t.Fatal("ShowDecision(absent key) error = nil")
	} else if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("ShowDecision(absent key) error = %v, want a not-found failure", err)
	}
}

// An unreadable inventory is a failure, never an empty one: "no questions are
// waiting" is the one answer a broken read must not be able to give.
func TestDecisionInventory_RefusesInvalidReferencesAndUnavailableReads(t *testing.T) {
	aired := time.Unix(1_800_000_000, 0).UTC()
	queries := decisionQueryFixture(t, &decisionInventoryStub{decisions: []TaskDecision{askedDecision(1, aired)}})
	if _, err := queries.ListDecisions(context.Background(), "not a handle"); err == nil {
		t.Error("ListDecisions(invalid handle) error = nil")
	}
	if _, err := queries.ShowDecision(context.Background(), "task-decision-0001", "not a key"); err == nil {
		t.Error("ShowDecision(invalid key) error = nil")
	}

	failing := decisionQueryFixture(t, &decisionInventoryStub{err: errors.New("durable read failed")})
	if _, err := failing.ListDecisions(context.Background(), ""); err == nil {
		t.Error("ListDecisions(store failure) error = nil")
	}

	unconfigured, err := NewQueries(QueryConfig{
		Repository: &queryRepository{},
		Clock:      func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	if _, err := unconfigured.ListDecisions(context.Background(), ""); err == nil {
		t.Error("ListDecisions(no inventory) error = nil")
	}
	if _, err := unconfigured.ShowDecision(context.Background(), "task-decision-0001", "schema-choice"); err == nil {
		t.Error("ShowDecision(no inventory) error = nil")
	}
}
