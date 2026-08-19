package localapi

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func eventQueriesFixture() *apiQueries {
	occurred := time.Unix(1_800_000_000, 0).UTC()
	return &apiQueries{events: application.EventPage{
		SchemaVersion: 1, CapturedAt: occurred, NextCursor: 9,
		Events: []application.ServiceEvent{
			{Sequence: 9, OccurredAt: occurred, Kind: application.EventDecisionOpened,
				TaskHandle: "task-0001", Reason: "schema-choice"},
		},
	}}
}

// The operator console follows the stream over the owner-only endpoint.
func TestClient_ReadsTheEventStream(t *testing.T) {
	client := newDecisionClient(t, CallerOperatorCLI, eventQueriesFixture())

	page, err := client.ReadEvents(context.Background(), "read-events", ReadEventsInput{AfterSequence: 4, Limit: 50})
	if err != nil {
		t.Fatalf("ReadEvents() error = %v", err)
	}
	if page.NextCursor != 9 || len(page.Events) != 1 {
		t.Fatalf("page = %#v", page)
	}
	if page.Events[0].Kind != application.EventDecisionOpened {
		t.Fatalf("event = %#v", page.Events[0])
	}
}

// The stream is content-free, so unlike the private task reads it is reachable
// from the model facade: §23.1 makes it the platform-wide view.
func TestHandler_AllowsTheContentFreeEventStreamFromBothCallers(t *testing.T) {
	for _, caller := range []CallerClass{CallerOperatorCLI, CallerMCPFacade} {
		client := newDecisionClient(t, caller, eventQueriesFixture())
		if _, err := client.ReadEvents(context.Background(), "read-events", ReadEventsInput{}); err != nil {
			t.Errorf("ReadEvents(%s) error = %v", caller, err)
		}
	}
}

func TestReadEventsMethod_IsAValidRead(t *testing.T) {
	if !MethodReadEvents.valid() || MethodReadEvents.SideEffect() != SideEffectRead {
		t.Fatalf("ReadEvents method = %q, side effect = %q", MethodReadEvents, MethodReadEvents.SideEffect())
	}
	if MethodReadEvents.operatorOnly() {
		t.Error("the content-free stream is needlessly operator-only")
	}
	for _, caller := range []CallerClass{CallerWorkerReport, CallerComisControl} {
		if methodAllowed(caller, MethodReadEvents) {
			t.Errorf("ReadEvents is reachable from %s", caller)
		}
	}
}
