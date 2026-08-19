package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func fixedAttestationTime() time.Time { return time.Unix(1_800_000_000, 0).UTC() }

// attestationFixture creates one task of the requested shape and nothing else,
// so each assertion is about the attestation gate rather than about the rest of
// a cleanup-ready task.
func attestationFixture(t *testing.T, shape domain.TaskShape) (*Store, domain.Task) {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-attest-0001", 1)
	task.Shape = shape
	task.State = domain.TaskWorking
	task.ManagedRunID = "managed-run-0001"
	task.WorkspaceLeaseID = "workspace-lease-0001"
	if shape == domain.ShapeScout {
		task.DeliveryMode = domain.DeliveryReport
	}
	// The brief hash pins the shape, so it is re-pinned after the shape moves.
	pinned, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	if err := store.CreateTask(context.Background(), pinned); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	return store, pinned
}

// proveAttestationGate runs the cleanup safety proof against one task.
func proveAttestationGate(t *testing.T, store *Store, task domain.Task) error {
	t.Helper()
	transaction, err := store.db.Begin()
	if err != nil {
		t.Fatalf("Begin() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	return proveCleanupDatabaseSafety(context.Background(), transaction, task)
}

func attestScout(
	t *testing.T,
	store *Store,
	task domain.Task,
	operationID string,
	finding application.ScoutAttestationFinding,
	keys []string,
) {
	t.Helper()
	if _, err := store.CommitScoutDecisionAttestation(context.Background(), application.ScoutDecisionAttestationMutation{
		OperationID: operationID, SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: task.Handle, Finding: finding, OpenDecisionKeys: keys,
		At: fixedAttestationTime(),
	}); err != nil {
		t.Fatalf("CommitScoutDecisionAttestation() error = %v", err)
	}
}

// A scout's worktree holds the only copy of its investigation. Removing it
// before anybody has inventoried the report's open questions is exactly how a
// buried question disappears with the tree that held it, so cleanup refuses
// until an inventory exists.
//
// The refusal turns on absence of a record, not on an empty one: "nobody
// looked" and "somebody looked and found nothing" are different facts, and only
// the second may authorize removal.
func TestCleanupSafety_RefusesAScoutNobodyInventoried(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)

	if err := proveAttestationGate(t, store, scout); !errors.Is(err, application.ErrCleanupUnattestedScout) {
		t.Fatalf("cleanup error = %v, want ErrCleanupUnattestedScout", err)
	}
}

// An inventory naming still-open decisions blocks removal for the same reason
// an unresolved decision does: the question survives the worktree only if the
// worktree survives the question.
func TestCleanupSafety_RefusesAScoutWithOpenDecisionsInItsInventory(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	attestScout(t, store, scout, "operation-attest-0001", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})

	if err := proveAttestationGate(t, store, scout); !errors.Is(err, application.ErrCleanupUnattestedScout) {
		t.Fatalf("cleanup error = %v, want ErrCleanupUnattestedScout", err)
	}
}

// A positive inventory is what removal waits for: a recorded judgement somebody
// can be held to, which is the whole difference from silence.
func TestCleanupSafety_AllowsAScoutWhoseInventoryClearedIt(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	attestScout(t, store, scout, "operation-attest-0001", application.ScoutAttestationNoOpenDecisions, nil)

	if err := proveAttestationGate(t, store, scout); errors.Is(err, application.ErrCleanupUnattestedScout) {
		t.Fatalf("a cleared inventory still blocked cleanup: %v", err)
	}
}

// A ship task has no investigation surface to inventory, so the gate does not
// apply. Its open decisions remain governed by the ordinary decision blocker.
func TestCleanupSafety_DoesNotDemandAnInventoryFromAShipTask(t *testing.T) {
	store, ship := attestationFixture(t, domain.ShapeShip)

	if err := proveAttestationGate(t, store, ship); errors.Is(err, application.ErrCleanupUnattestedScout) {
		t.Fatalf("a ship task was asked for a scout inventory: %v", err)
	}
}

// The recorded inventory reads back exactly, its absence reports as absent
// rather than as an empty finding, and a later look replaces an earlier one so
// a stale inventory can never outvote a fresher inspection.
func TestReadScoutDecisionInventory_SeparatesAbsenceFromAnEmptyFinding(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)

	if _, found, err := store.ReadScoutDecisionInventory(context.Background(), scout.Handle); err != nil || found {
		t.Fatalf("ReadScoutDecisionInventory(unattested) = found %v, %v", found, err)
	}

	attestScout(t, store, scout, "operation-attest-0001",
		application.ScoutAttestationOpenDecisions, []string{"schema-choice", "rollout-window"})
	inventory, found, err := store.ReadScoutDecisionInventory(context.Background(), scout.Handle)
	if err != nil || !found {
		t.Fatalf("ReadScoutDecisionInventory() = found %v, %v", found, err)
	}
	if inventory.Finding != application.ScoutAttestationOpenDecisions ||
		len(inventory.OpenDecisionKeys) != 2 || inventory.OpenDecisionKeys[0] != "schema-choice" {
		t.Fatalf("inventory = %#v", inventory)
	}
	if inventory.AttestedAt.IsZero() {
		t.Error("inventory carries no attestation time")
	}

	attestScout(t, store, scout, "operation-attest-0002", application.ScoutAttestationNoOpenDecisions, nil)
	cleared, _, err := store.ReadScoutDecisionInventory(context.Background(), scout.Handle)
	if err != nil || !cleared.ClearsReview() || len(cleared.OpenDecisionKeys) != 0 {
		t.Fatalf("replaced inventory = %#v, %v", cleared, err)
	}
}

// Only a scout can be inventoried. A record against a ship task would be one no
// gate reads and that misdescribes the work it names.
func TestCommitScoutDecisionAttestation_RefusesAShipTask(t *testing.T) {
	store, ship := attestationFixture(t, domain.ShapeShip)

	_, err := store.CommitScoutDecisionAttestation(context.Background(), application.ScoutDecisionAttestationMutation{
		OperationID: "operation-attest-0001", SubjectDigest: strings.Repeat("e", 64),
		TaskHandle: ship.Handle, Finding: application.ScoutAttestationNoOpenDecisions,
		At: fixedAttestationTime(),
	})
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitScoutDecisionAttestation(ship) error = %v, want a precondition failure", err)
	}
}

// A stored inventory that cannot be decoded is a failure, not an empty one.
// Reading a corrupt row as "no keys" would quietly clear a scout whose
// inventory actually named open questions.
func TestReadScoutDecisionInventory_RefusesACorruptRecord(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	attestScout(t, store, scout, "operation-attest-0001", application.ScoutAttestationOpenDecisions, []string{"schema-choice"})

	for label, corrupt := range map[string]struct{ column, value string }{
		"unreadable keys": {"open_decision_keys", "{not-json"},
		"unreadable time": {"attested_at", "not-a-time"},
	} {
		if _, err := store.db.Exec(
			"UPDATE scout_decision_attestations SET "+corrupt.column+" = ? WHERE task_handle = ?",
			corrupt.value, scout.Handle,
		); err != nil {
			t.Fatalf("corrupt %s: %v", label, err)
		}
		if _, _, err := store.ReadScoutDecisionInventory(context.Background(), scout.Handle); err == nil {
			t.Errorf("ReadScoutDecisionInventory(%s) error = nil", label)
		}
		// Restore the column so the next case corrupts exactly one field.
		attestScout(t, store, scout, "operation-attest-0002",
			application.ScoutAttestationOpenDecisions, []string{"schema-choice"})
	}
}

// A caller with no context, or one already canceled, has not asked a question
// the store may answer.
func TestReadScoutDecisionInventory_RefusesMissingAuthority(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)

	if _, _, err := store.ReadScoutDecisionInventory(missingStoreContext(), scout.Handle); err == nil {
		t.Error("ReadScoutDecisionInventory(no context) error = nil")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.ReadScoutDecisionInventory(canceled, scout.Handle); !errors.Is(err, context.Canceled) {
		t.Errorf("ReadScoutDecisionInventory(canceled) error = %v", err)
	}
}

// missingStoreContext hands back an absent context through a function so the
// call site states the refusal under test rather than tripping a vet rule.
func missingStoreContext() context.Context { return nil }

// The ledger counts per decision and keeps the first sighting, so the age of an
// unanswered question stays legible however many times it has been raised.
func TestDecisionSurfacingLedger_CountsPerDecisionAndKeepsTheFirstSighting(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	first := fixedAttestationTime()

	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: first,
	}); err != nil {
		t.Fatalf("RecordDecisionSurfaced() error = %v", err)
	}
	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: first.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RecordDecisionSurfaced(again) error = %v", err)
	}

	var count int
	var firstSeen, lastSeen string
	if err := store.db.QueryRow(
		`SELECT surface_count, first_surfaced_at, last_surfaced_at
         FROM task_decision_surfacings WHERE task_handle = ? AND external_key = ?`,
		scout.Handle, "schema-choice",
	).Scan(&count, &firstSeen, &lastSeen); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if count != 2 {
		t.Errorf("surface count = %d, want 2", count)
	}
	if firstSeen == lastSeen {
		t.Error("a second surfacing must move the last sighting without moving the first")
	}
}

// A decision with no resolution is open; one whose key was resolved is not. The
// same predicate governs cleanup, so the two can never disagree about which
// questions are still live.
func TestOpenDecisionsAwaitingHuman_ExcludesResolvedKeysAndCarriesTheLedger(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)

	open, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(open) != 0 {
		t.Fatalf("a task with no decisions reported %d open", len(open))
	}

	if _, err := store.OpenDecisionsAwaitingHuman(missingStoreContext()); err == nil {
		t.Error("OpenDecisionsAwaitingHuman(no context) error = nil")
	}
	if err := store.RecordDecisionSurfaced(missingStoreContext(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice",
	}); err == nil {
		t.Error("RecordDecisionSurfaced(no context) error = nil")
	}
}

// A real decision report is open until a resolution carries its key, and the
// ledger travels with it so the cadence survives a restart.
//
// Delivering the report is what asks the question, so the count starts at one
// airing and the ledger adds the repeats on top of it.
func TestOpenDecisionsAwaitingHuman_TracksARealDecisionThroughItsResolution(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	at := scout.UpdatedAt.Add(time.Minute)

	reportDecision(t, store, scout, "schema-choice", at)
	askTheHuman(t, store, at.Add(time.Second))

	open, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatalf("OpenDecisionsAwaitingHuman() error = %v", err)
	}
	if len(open) != 1 || open[0].ExternalKey != "schema-choice" || open[0].SurfaceCount != 1 {
		t.Fatalf("open decisions = %+v", open)
	}

	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: at.Add(time.Hour),
	}); err != nil {
		t.Fatalf("RecordDecisionSurfaced() error = %v", err)
	}
	raised, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(raised) != 1 || raised[0].SurfaceCount != 2 || raised[0].LastSurfacedAt.IsZero() {
		t.Fatalf("raised decision = %+v", raised)
	}

	resolution := sqliteWorkerReport(scout, "report-resolution-0001", domain.ReportResolution)
	resolution.ExternalKey = "schema-choice"
	if _, err := store.CommitReport(context.Background(), directReportMutation(scout, resolution, at.Add(2*time.Hour))); err != nil {
		t.Fatalf("CommitReport(resolution) error = %v", err)
	}
	settled, err := store.OpenDecisionsAwaitingHuman(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(settled) != 0 {
		t.Fatalf("a resolved decision is still reported open: %+v", settled)
	}
}

// A canceled caller has withdrawn the question, and a ledger row whose time
// cannot be read is a failure rather than a decision that was never raised —
// treating it as never-raised would restart the cadence from the beginning.
func TestDecisionSurfacing_RefusesCanceledCallersAndCorruptLedgerRows(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := store.OpenDecisionsAwaitingHuman(canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("OpenDecisionsAwaitingHuman(canceled) error = %v", err)
	}
	if err := store.RecordDecisionSurfaced(canceled, application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice",
	}); !errors.Is(err, context.Canceled) {
		t.Errorf("RecordDecisionSurfaced(canceled) error = %v", err)
	}

	at := scout.UpdatedAt.Add(time.Minute)
	decision := sqliteWorkerReport(scout, "report-decision-0002", domain.ReportDecision)
	decision.ExternalKey = "schema-choice"
	if _, err := store.CommitReport(context.Background(), directReportMutation(scout, decision, at)); err != nil {
		t.Fatalf("CommitReport(decision) error = %v", err)
	}
	askTheHuman(t, store, at.Add(time.Second))
	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: at,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(
		`UPDATE task_decision_surfacings SET last_surfaced_at = 'not-a-time' WHERE task_handle = ?`,
		scout.Handle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenDecisionsAwaitingHuman(context.Background()); err == nil {
		t.Error("OpenDecisionsAwaitingHuman(corrupt ledger time) error = nil")
	}
}

// A database that has gone away is reported as a failure. Returning an empty
// set instead would read as "no questions are open", which is the one answer a
// broken store must never be able to give.
func TestDecisionSurfacing_ReportsAnUnavailableDatabase(t *testing.T) {
	store, scout := attestationFixture(t, domain.ShapeScout)
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if _, err := store.OpenDecisionsAwaitingHuman(context.Background()); err == nil {
		t.Error("OpenDecisionsAwaitingHuman(closed store) error = nil")
	}
	if err := store.RecordDecisionSurfaced(context.Background(), application.DecisionSurfacedMutation{
		TaskHandle: scout.Handle, ExternalKey: "schema-choice", At: fixedAttestationTime(),
	}); err == nil {
		t.Error("RecordDecisionSurfaced(closed store) error = nil")
	}
	if _, _, err := store.ReadScoutDecisionInventory(context.Background(), scout.Handle); err == nil {
		t.Error("ReadScoutDecisionInventory(closed store) error = nil")
	}
}
