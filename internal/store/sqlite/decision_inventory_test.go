package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The operator inventory separates a question the host has not been told about
// from one the human has been asked and has not answered. Collapsing the two
// would make a stuck outbox look like an unresponsive human.
func TestListTaskDecisions_SeparatesUnaskedFromUnanswered(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)

	unasked, err := store.ListTaskDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTaskDecisions() error = %v", err)
	}
	if len(unasked) != 1 {
		t.Fatalf("decisions = %d, want one", len(unasked))
	}
	if unasked[0].Status != application.DecisionAwaitingHost {
		t.Errorf("status = %q, want %q", unasked[0].Status, application.DecisionAwaitingHost)
	}
	if unasked[0].AskedAt != nil {
		t.Errorf("asked at = %v, want none until the host acknowledges", unasked[0].AskedAt)
	}
	if !unasked[0].ReportedAt.Equal(at) {
		t.Errorf("reported at = %s, want %s", unasked[0].ReportedAt, at)
	}
	if unasked[0].Question != "which migration order applies" {
		t.Errorf("question = %q", unasked[0].Question)
	}
	if unasked[0].Detail != "the two candidate orders differ in their backfill window" {
		t.Errorf("detail = %q", unasked[0].Detail)
	}

	asked := at.Add(time.Second)
	askTheHuman(t, store, asked)
	answered, err := store.ListTaskDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTaskDecisions(asked) error = %v", err)
	}
	if len(answered) != 1 || answered[0].Status != application.DecisionAwaitingHuman {
		t.Fatalf("decisions = %+v, want one awaiting the human", answered)
	}
	if answered[0].AskedAt == nil || !answered[0].AskedAt.Equal(asked) {
		t.Errorf("asked at = %v, want %s", answered[0].AskedAt, asked)
	}
	if answered[0].Airings != 1 {
		t.Errorf("airings = %d, want the delivery counted", answered[0].Airings)
	}
}

// A resolved decision leaves the inventory: it is no longer something anybody
// is waiting on, and listing it would bury the questions that still are.
func TestListTaskDecisions_DropsAResolvedKeyAndScopesToOneTask(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))

	scoped, err := store.ListTaskDecisions(context.Background(), scout.Handle)
	if err != nil {
		t.Fatalf("ListTaskDecisions(scoped) error = %v", err)
	}
	if len(scoped) != 1 {
		t.Fatalf("scoped decisions = %d, want one", len(scoped))
	}
	elsewhere, err := store.ListTaskDecisions(context.Background(), "task-elsewhere-0001")
	if err != nil {
		t.Fatalf("ListTaskDecisions(other task) error = %v", err)
	}
	if len(elsewhere) != 0 {
		t.Fatalf("another task's scope returned %d decisions", len(elsewhere))
	}

	resolution := sqliteWorkerReport(scout, "report-resolution-0001", domain.ReportResolution)
	resolution.ExternalKey = "schema-choice"
	if _, err := store.CommitReport(
		context.Background(), directReportMutation(scout, resolution, at.Add(2*time.Hour)),
	); err != nil {
		t.Fatalf("CommitReport(resolution) error = %v", err)
	}
	settled, err := store.ListTaskDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListTaskDecisions(resolved) error = %v", err)
	}
	if len(settled) != 0 {
		t.Fatalf("a resolved decision is still inventoried: %+v", settled)
	}
}

// A read that cannot be completed is a failure. An empty inventory would read as
// "nothing is waiting on anybody", which is the one answer a broken read must
// never be able to give.
func TestListTaskDecisions_RefusesACanceledCallerAndAnUnavailableDatabase(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.ListTaskDecisions(canceled, ""); err == nil {
		t.Error("ListTaskDecisions(canceled) error = nil")
	}
	if _, err := store.ListTaskDecisions(missingStoreContext(), ""); err == nil {
		t.Error("ListTaskDecisions(no context) error = nil")
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.ListTaskDecisions(context.Background(), scout.Handle); err == nil {
		t.Error("ListTaskDecisions(closed store) error = nil")
	}
}

// A stored time that cannot be read is a failure, never a decision with no
// history. Treating an unreadable row as "never asked" would restart the cadence
// and re-ask a question the human has already seen.
func TestListTaskDecisions_RefusesRowsWhoseStoredTimesCannotBeRead(t *testing.T) {
	for name, corrupt := range map[string]string{
		"report time":    `UPDATE reports SET accepted_at = 'not-a-time' WHERE kind = 'decision'`,
		"delivery time":  `UPDATE comis_report_outbox SET delivered_at = 'not-a-time'`,
		"surfacing time": `UPDATE task_decision_surfacings SET last_surfaced_at = 'not-a-time'`,
	} {
		t.Run(name, func(t *testing.T) {
			store, scout := attestationFixture(t, domain.ShapeScout)
			at := scout.UpdatedAt.Add(time.Minute)
			reportDecision(t, store, scout, "schema-choice", at)
			askTheHuman(t, store, at.Add(time.Second))
			if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
				TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: at.Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.db.Exec(corrupt); err != nil {
				t.Fatalf("corrupt %s: %v", name, err)
			}
			if _, err := store.ListTaskDecisions(context.Background(), ""); err == nil {
				t.Fatalf("ListTaskDecisions(corrupt %s) error = nil, want a refusal", name)
			}
			if _, err := store.OpenDecisionsAwaitingHuman(context.Background()); err == nil && name != "report time" {
				t.Fatalf("OpenDecisionsAwaitingHuman(corrupt %s) error = nil, want a refusal", name)
			}
		})
	}
}

// A row whose columns cannot be decoded is a failure rather than a skipped
// decision, so a schema drift can never quietly shorten the inventory.
func TestListTaskDecisions_RefusesARowItCannotDecode(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)
	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))
	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: at.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`UPDATE task_decision_surfacings SET surface_count = 'not-a-count'`); err != nil {
		t.Fatalf("corrupt surfacing count: %v", err)
	}
	if _, err := store.ListTaskDecisions(context.Background(), ""); err == nil {
		t.Fatal("ListTaskDecisions(undecodable row) error = nil, want a refusal")
	}
}
