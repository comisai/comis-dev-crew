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

// The link references the operation that created the ship task, so in the real
// flow the operation already exists by the time the link is written. The fixture
// records it the same way rather than relaxing the reference.
func recordPromotionOperation(t *testing.T, store *Store, operationID, taskHandle string, at time.Time) {
	t.Helper()
	operation := completedMutationOperation(
		operationID, commandPrepareTask, strings.Repeat("9", 64), taskHandle, 1, at,
	)
	if err := insertOperation(context.Background(), store.db, operation); err != nil {
		t.Fatalf("insertOperation() error = %v", err)
	}
}

func scoutWithEvidence(t *testing.T, store *Store, handle string) domain.Task {
	t.Helper()
	scout := storeTask(handle, 1)
	scout.Shape = domain.ShapeScout
	scout.DeliveryMode = domain.DeliveryReport
	// The brief hash pins the shape and contract together, so changing the shape
	// invalidates it. Re-pinning here is what a real preparation does.
	scout, err := scout.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	if err := store.CreateTask(context.Background(), scout); err != nil {
		t.Fatalf("CreateTask(scout) error = %v", err)
	}
	return scout
}

// A promotion points at an investigation. A scout that produced no sealed
// evidence investigated nothing, so there is nothing for a ship task to be
// justified by and nothing to preserve — the link would name an empty report.
func TestStore_ScoutPromotionSourceRefusesAScoutWithNoEvidence(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	scout := scoutWithEvidence(t, store, "task-scout-0001")

	_, err := store.ReadScoutPromotionSource(context.Background(), scout.Handle)

	if !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadScoutPromotionSource(no evidence) error = %v, want not-found", err)
	}
}

// Only a scout can be promoted. Promoting a ship task would create a second ship
// task claiming the first one's evidence as the investigation that justifies it.
func TestStore_ScoutPromotionSourceRefusesATaskThatIsNotAScout(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))

	_, err := store.ReadScoutPromotionSource(context.Background(), ship.Handle)

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReadScoutPromotionSource(ship) error = %v, want a precondition refusal", err)
	}
}

// A repeat of the same promotion is safe and must not conflict. A repeat that
// names a different scout or ship task under the same operation identity is a
// different promotion wearing a borrowed name, and committing it would attribute
// one investigation's evidence to work it never covered.
func TestStore_ScoutPromotionLinkReplaysIdenticallyAndRefusesAnAlteredRepeat(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	scout := scoutWithEvidence(t, store, "task-scout-0001")
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	recordPromotionOperation(t, store, "operation-promote-0001", ship.Handle, at)
	link := application.ScoutPromotionLink{
		OperationID: "operation-promote-0001", ScoutTaskHandle: scout.Handle,
		ShipTaskHandle: ship.Handle, EvidenceDigest: strings.Repeat("f", 64), PromotedAt: at,
	}

	if err := store.CommitScoutPromotionLink(context.Background(), link); err != nil {
		t.Fatalf("CommitScoutPromotionLink() error = %v", err)
	}
	if err := store.CommitScoutPromotionLink(context.Background(), link); err != nil {
		t.Fatalf("CommitScoutPromotionLink(replay) error = %v", err)
	}

	altered := link
	altered.EvidenceDigest = strings.Repeat("0", 64)
	if err := store.CommitScoutPromotionLink(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CommitScoutPromotionLink(altered) error = %v, want a conflict", err)
	}
}

func TestStore_ScoutPromotionLinkIsReadableFromTheShipTask(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	scout := scoutWithEvidence(t, store, "task-scout-0001")
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	recordPromotionOperation(t, store, "operation-promote-0001", ship.Handle, at)
	if err := store.CommitScoutPromotionLink(context.Background(), application.ScoutPromotionLink{
		OperationID: "operation-promote-0001", ScoutTaskHandle: scout.Handle,
		ShipTaskHandle: ship.Handle, EvidenceDigest: strings.Repeat("f", 64), PromotedAt: at,
	}); err != nil {
		t.Fatalf("CommitScoutPromotionLink() error = %v", err)
	}

	link, found, err := store.ScoutPromotion(context.Background(), ship.Handle)
	if err != nil || !found {
		t.Fatalf("ScoutPromotion() = %+v, %t, %v", link, found, err)
	}
	if link.ScoutTaskHandle != scout.Handle || link.EvidenceDigest != strings.Repeat("f", 64) {
		t.Errorf("promotion link = %+v", link)
	}
	if !link.PromotedAt.Equal(at) {
		t.Errorf("promoted at = %v, want %v", link.PromotedAt, at)
	}
}

// A ship task nobody promoted reports no link rather than an error: most ship
// tasks are written directly, and treating that as a fault would make the read
// unusable for the common case.
func TestStore_AShipTaskWithNoPromotionReportsNoLink(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))

	link, found, err := store.ScoutPromotion(context.Background(), ship.Handle)

	if err != nil {
		t.Fatalf("ScoutPromotion() error = %v", err)
	}
	if found {
		t.Errorf("unexpected promotion link = %+v", link)
	}
}

func TestStore_ScoutPromotionLinkRefusesANonUTCTime(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	scout := scoutWithEvidence(t, store, "task-scout-0001")
	recordPromotionOperation(t, store, "operation-promote-0001", ship.Handle,
		time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC))

	err := store.CommitScoutPromotionLink(context.Background(), application.ScoutPromotionLink{
		OperationID: "operation-promote-0001", ScoutTaskHandle: scout.Handle,
		ShipTaskHandle: ship.Handle, EvidenceDigest: strings.Repeat("f", 64),
		PromotedAt: time.Date(2026, time.August, 9, 16, 0, 0, 0, time.FixedZone("local", 3600)),
	})

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitScoutPromotionLink(local time) error = %v, want a precondition refusal", err)
	}
}

// The read that makes a promotion possible: a scout that produced sealed
// evidence hands the ship revision its repository, its base revision and the
// exact digest that justifies it.
func TestStore_ScoutPromotionSourceCarriesTheInvestigationsGroundAndDigest(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()
	evidenceStore, ok := any(store).(candidateEvidenceStore)
	if !ok {
		t.Fatal("Store does not implement durable candidate evidence")
	}
	scout := candidateEvidenceTask(t, "task-scout-source")
	scout.Shape = domain.ShapeScout
	scout.DeliveryMode = domain.DeliveryReport
	scout, err = scout.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	if err := store.CreateTask(context.Background(), scout); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	sealed := candidateEvidence(t, scout, strings.Repeat("b", 40))
	if _, _, err := evidenceStore.CommitCandidateEvidence(
		context.Background(), scout.Handle, sealed, []string{"unit"}, nil,
		scout.UpdatedAt.Add(5*time.Minute), candidateEvidencePublications(t, scout, sealed),
	); err != nil {
		t.Fatalf("CommitCandidateEvidence() error = %v", err)
	}

	source, err := store.ReadScoutPromotionSource(context.Background(), scout.Handle)
	if err != nil {
		t.Fatalf("ReadScoutPromotionSource() error = %v", err)
	}
	if source.ScoutTaskHandle != scout.Handle || source.RepositoryID != scout.RepositoryID ||
		source.BaseRevision != scout.BaseRevision {
		t.Errorf("promotion source = %+v, want the scout's own ground", source)
	}
	if source.EvidenceDigest != sealed.Digest() {
		t.Errorf("promotion source digest = %q, want the sealed evidence digest", source.EvidenceDigest)
	}
}

// Every promotion entry point refuses an absent context before it touches the
// database, so a caller that lost its context cannot reach durable state.
func TestStore_PromotionEntryPointsRefuseAnAbsentContext(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))

	if _, err := store.ReadScoutPromotionSource(nilPromotionContext(), ship.Handle); err == nil {
		t.Error("ReadScoutPromotionSource(nil) error = nil")
	}
	if err := store.CommitScoutPromotionLink(nilPromotionContext(), application.ScoutPromotionLink{}); err == nil {
		t.Error("CommitScoutPromotionLink(nil) error = nil")
	}
	if _, _, err := store.ScoutPromotion(nilPromotionContext(), ship.Handle); err == nil {
		t.Error("ScoutPromotion(nil) error = nil")
	}
}

// Returned through a function so the nil is not a literal argument at the call
// site, matching how this package's own tests exercise the guard.
func nilPromotionContext() context.Context { return nil }

// One ship task cannot come from two promotions. If it could, two
// investigations would each claim to be the justification for the same work and
// nothing could say which the operator actually acted on.
func TestStore_AShipTaskCannotBeClaimedByTwoPromotions(t *testing.T) {
	store, ship := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	scout := scoutWithEvidence(t, store, "task-scout-0001")
	other := scoutWithEvidence(t, store, "task-scout-0002")
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	recordPromotionOperation(t, store, "operation-promote-0001", ship.Handle, at)
	recordPromotionOperation(t, store, "operation-promote-0002", ship.Handle, at)

	if err := store.CommitScoutPromotionLink(context.Background(), application.ScoutPromotionLink{
		OperationID: "operation-promote-0001", ScoutTaskHandle: scout.Handle,
		ShipTaskHandle: ship.Handle, EvidenceDigest: strings.Repeat("f", 64), PromotedAt: at,
	}); err != nil {
		t.Fatalf("CommitScoutPromotionLink() error = %v", err)
	}

	err := store.CommitScoutPromotionLink(context.Background(), application.ScoutPromotionLink{
		OperationID: "operation-promote-0002", ScoutTaskHandle: other.Handle,
		ShipTaskHandle: ship.Handle, EvidenceDigest: strings.Repeat("e", 64), PromotedAt: at,
	})

	if !errors.Is(err, application.ErrConflict) {
		t.Fatalf("second promotion of the same ship task error = %v, want a conflict", err)
	}
}
