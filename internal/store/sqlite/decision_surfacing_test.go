package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// reportDecision commits one keyed decision report against the fixture task.
func reportDecision(t *testing.T, store *Store, task domain.Task, key string, at time.Time) domain.WorkerReport {
	t.Helper()
	decision := sqliteWorkerReport(task, "report-decision-0001", domain.ReportDecision)
	decision.ExternalKey = key
	decision.Summary = "which migration order applies"
	decision.Details = "the two candidate orders differ in their backfill window"
	if _, err := store.CommitReport(context.Background(), directReportMutation(task, decision, at)); err != nil {
		t.Fatalf("CommitReport(decision) error = %v", err)
	}
	return decision
}

// askTheHuman drains the pending report to the host, which is the first time the
// question is actually put in front of anybody.
func askTheHuman(t *testing.T, store *Store, at time.Time) {
	t.Helper()
	delivery, found, err := store.NextComisReport(context.Background())
	if err != nil || !found {
		t.Fatalf("NextComisReport() = %#v, %t, %v", delivery, found, err)
	}
	if err := store.MarkComisReportDelivered(context.Background(), delivery.OperationID, application.ComisReportAcknowledgement{
		ManagedRunID: delivery.ManagedRunID, ServiceReportID: delivery.ServiceReportID,
		AcceptedSequence: 1, RetainedUntil: at.Add(24 * time.Hour),
	}, at); err != nil {
		t.Fatalf("MarkComisReportDelivered() error = %v", err)
	}
}

// Re-raising a question means sending it again, so the ledger read has to carry
// the question itself and the run it belongs to. Reading those separately would
// let the two halves disagree: a decision could be selected as due and then
// raised against a run identity read a moment later, after cleanup released it.
func TestOpenDecisionsAwaitingHuman_CarriesWhatRaisingItAgainNeeds(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	decision := reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))

	open, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open decisions = %d, want one", len(open))
	}
	if open[0].ManagedRunID != scout.ManagedRunID {
		t.Errorf("managed run = %q, want %q", open[0].ManagedRunID, scout.ManagedRunID)
	}
	if open[0].Summary != decision.Summary {
		t.Errorf("summary = %q, want %q", open[0].Summary, decision.Summary)
	}
	if open[0].Details != decision.Details {
		t.Errorf("details = %q, want %q", open[0].Details, decision.Details)
	}
}

// A decision on a task with no managed run cannot be raised to anybody. The
// read reports it rather than emitting a row whose run identity is empty, which
// a raiser would then have to reject one layer further out.
func TestOpenDecisionsAwaitingHuman_RefusesADecisionWithNoManagedRun(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	if _, err := store.db.Exec(`UPDATE tasks SET managed_run_id = '' WHERE handle = ?`, scout.Handle); err != nil {
		t.Fatalf("clear managed run: %v", err)
	}

	if _, err := store.OpenDecisionsAwaitingHuman(context.Background()); err == nil {
		t.Fatal("OpenDecisionsAwaitingHuman() error = nil, want a refusal")
	} else if !strings.Contains(err.Error(), "managed run") {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v, want it to name the missing run identity", err)
	}
}

// A question the host has not been told about yet is not owed a repeat. Its
// first airing is the delivery of the decision report itself, so re-surfacing a
// still-pending one would ask the liaison twice for the same question — once
// through the outbox and once through the cadence.
func TestOpenDecisionsAwaitingHuman_WaitsUntilTheQuestionHasBeenAskedOnce(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)

	pending, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("an unasked question is already owed a repeat: %+v", pending)
	}

	delivered := at.Add(time.Second)
	askTheHuman(t, store, delivered)
	asked, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman(asked) error = %v", err)
	}
	if len(asked) != 1 {
		t.Fatalf("open decisions = %d, want one", len(asked))
	}
	if asked[0].SurfaceCount != 1 {
		t.Errorf("surface count = %d, want the delivery counted as the first airing", asked[0].SurfaceCount)
	}
	if !asked[0].LastSurfacedAt.Equal(delivered) {
		t.Errorf("last surfaced = %s, want the delivery time %s", asked[0].LastSurfacedAt, delivered)
	}
}

// The ledger counts the repeats on top of that first airing, and the later of
// the two sightings is what the cadence measures from. Taking the ledger alone
// would restart the interval at the original wake every time.
func TestOpenDecisionsAwaitingHuman_MeasuresFromTheMostRecentAiring(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))

	repeated := at.Add(time.Hour)
	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: repeated,
	}); err != nil {
		t.Fatalf("RecordDecisionSurfaced() error = %v", err)
	}

	open, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(open) != 1 || open[0].SurfaceCount != 2 {
		t.Fatalf("open decisions = %+v, want one counted twice", open)
	}
	if !open[0].LastSurfacedAt.Equal(repeated) {
		t.Errorf("last surfaced = %s, want the repeat at %s", open[0].LastSurfacedAt, repeated)
	}
}
