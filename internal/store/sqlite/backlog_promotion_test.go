package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestBacklogPromotionReservationAndResultSurviveRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	dependency := backlogAdditionItem("backlog-promoted-dependency", nil)
	dependency.Readiness = domain.BacklogPromoted
	target := backlogAdditionItem("backlog-ready-target", []string{dependency.Handle})
	if err := store.CreateBacklogItem(ctx, dependency); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBacklogItem(ctx, target); err != nil {
		t.Fatal(err)
	}
	reservation := application.BacklogPromotionReservation{
		OperationID: "operation-promote-backlog", SubjectDigest: strings.Repeat("a", 64),
		BacklogHandle: target.Handle, TaskOperationID: "backlog-task-operation",
		ReservedAt: backlogPromotionTime(),
	}
	reserved, err := store.ReserveBacklogPromotion(ctx, reservation)
	if err != nil {
		t.Fatalf("ReserveBacklogPromotion() error = %v", err)
	}
	if !reflect.DeepEqual(reserved.Item, target) ||
		!reflect.DeepEqual(reserved.SatisfiedDependencies, []string{dependency.Handle}) {
		t.Fatalf("ReserveBacklogPromotion() = %#v", reserved)
	}
	replayedReservation, err := store.ReserveBacklogPromotion(ctx, reservation)
	if err != nil || !reflect.DeepEqual(replayedReservation, reserved) {
		t.Fatalf("ReserveBacklogPromotion(replay) = %#v, %v", replayedReservation, err)
	}
	competing := reservation
	competing.OperationID = "operation-promote-competing"
	competing.SubjectDigest = strings.Repeat("b", 64)
	competing.TaskOperationID = "backlog-task-competing"
	if _, err := store.ReserveBacklogPromotion(ctx, competing); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReserveBacklogPromotion(competing) error = %v, want ErrConflict", err)
	}

	mutations := sqliteMutations(t, store, &sequenceIDs{ids: []string{"task-from-backlog"}}, backlogPromotionTime())
	prepare := sqlitePrepareCommand()
	prepare.OperationID = reservation.TaskOperationID
	prepare.RepositoryID = target.RepositoryID
	prepare.Shape = target.Shape
	prepare.AcceptanceCriteria = []string{target.RequestedOutcome, "The implementation is verified."}
	prepared, err := mutations.PrepareTask(ctx, prepare)
	if err != nil {
		t.Fatalf("PrepareTask() error = %v", err)
	}
	committed, err := store.CommitBacklogPromotion(ctx, application.BacklogPromotionMutation{
		Reservation: reserved, Prepared: prepared, At: backlogPromotionTime().Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CommitBacklogPromotion() error = %v", err)
	}
	if committed.Item.Readiness != domain.BacklogPromoted || committed.Task.Handle != prepared.Task.Handle ||
		committed.Preparation == nil || !reflect.DeepEqual(*committed.Preparation, *prepared.Preparation) ||
		committed.Operation.Command != "PromoteBacklog" || committed.Operation.ResultRef != prepared.Task.Handle {
		t.Fatalf("CommitBacklogPromotion() = %#v", committed)
	}
	replayed, err := store.CommitBacklogPromotion(ctx, application.BacklogPromotionMutation{
		Reservation: reserved, Prepared: prepared, At: backlogPromotionTime().Add(time.Minute),
	})
	if err != nil || !reflect.DeepEqual(replayed, committed) {
		t.Fatalf("CommitBacklogPromotion(replay) = %#v, %v", replayed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, found, err := reopened.ReplayBacklogPromotion(
		ctx, reservation.OperationID, reservation.SubjectDigest,
	)
	if err != nil || !found || !reflect.DeepEqual(restarted, committed) {
		t.Fatalf("ReplayBacklogPromotion(restart) = %#v, %t, %v", restarted, found, err)
	}
	if _, _, err := reopened.ReplayBacklogPromotion(
		ctx, reservation.OperationID, strings.Repeat("c", 64),
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayBacklogPromotion(altered) error = %v, want ErrConflict", err)
	}
}

func TestBacklogPromotionRefusesUnsatisfiedDependencyBeforeReservation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	dependency := backlogAdditionItem("backlog-dependency-not-promoted", nil)
	target := backlogAdditionItem("backlog-blocked-target", []string{dependency.Handle})
	if err := store.CreateBacklogItem(ctx, dependency); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateBacklogItem(ctx, target); err != nil {
		t.Fatal(err)
	}
	reservation := application.BacklogPromotionReservation{
		OperationID: "operation-promote-blocked", SubjectDigest: strings.Repeat("d", 64),
		BacklogHandle: target.Handle, TaskOperationID: "backlog-task-blocked",
		ReservedAt: backlogPromotionTime(),
	}
	if _, err := store.ReserveBacklogPromotion(ctx, reservation); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReserveBacklogPromotion(blocked) error = %v, want ErrPrecondition", err)
	}
	var reservations int
	if err := store.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM backlog_promotions WHERE backlog_handle = ?", target.Handle,
	).Scan(&reservations); err != nil {
		t.Fatal(err)
	}
	if reservations != 0 {
		t.Fatalf("blocked promotion reservations = %d, want 0", reservations)
	}
}

func backlogPromotionTime() time.Time {
	return time.Date(2026, time.August, 20, 22, 0, 0, 0, time.UTC)
}
