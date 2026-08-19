package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Re-raising a question means sending it again, so the ledger read has to carry
// the question itself and the run it belongs to. Reading those separately would
// let the two halves disagree: a decision could be selected as due and then
// raised against a run identity read a moment later, after cleanup released it.
func TestOpenDecisionsAwaitingHuman_CarriesWhatRaisingItAgainNeeds(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)

	decision := sqliteWorkerReport(scout, "report-decision-0001", domain.ReportDecision)
	decision.ExternalKey = "schema-choice"
	decision.Summary = "which migration order applies"
	decision.Details = "the two candidate orders differ in their backfill window"
	if _, err := store.CommitReport(context.Background(), directReportMutation(scout, decision, at)); err != nil {
		t.Fatalf("CommitReport(decision) error = %v", err)
	}

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

	decision := sqliteWorkerReport(scout, "report-decision-0001", domain.ReportDecision)
	decision.ExternalKey = "schema-choice"
	if _, err := store.CommitReport(context.Background(), directReportMutation(scout, decision, at)); err != nil {
		t.Fatalf("CommitReport(decision) error = %v", err)
	}
	if _, err := store.db.Exec(`UPDATE tasks SET managed_run_id = '' WHERE handle = ?`, scout.Handle); err != nil {
		t.Fatalf("clear managed run: %v", err)
	}

	if _, err := store.OpenDecisionsAwaitingHuman(context.Background()); err == nil {
		t.Fatal("OpenDecisionsAwaitingHuman() error = nil, want a refusal")
	} else if !strings.Contains(err.Error(), "managed run") {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v, want it to name the missing run identity", err)
	}
}
