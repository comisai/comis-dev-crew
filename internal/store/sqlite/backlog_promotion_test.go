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
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	retryReservation := reservation
	retryReservation.ReservedAt = reservation.ReservedAt.Add(time.Hour)
	restartedReservation, err := store.ReserveBacklogPromotion(ctx, retryReservation)
	if err != nil || !reflect.DeepEqual(restartedReservation, reserved) {
		t.Fatalf("ReserveBacklogPromotion(restart) = %#v, %v", restartedReservation, err)
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
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_backlog_promotion_operation
		BEFORE INSERT ON operations WHEN NEW.command = 'PromoteBacklog'
		BEGIN SELECT RAISE(ABORT, 'injected backlog promotion failure'); END`); err != nil {
		t.Fatal(err)
	}
	mutation := application.BacklogPromotionMutation{
		Reservation: reserved, Prepared: prepared, At: backlogPromotionTime().Add(time.Minute),
	}
	if _, err := store.CommitBacklogPromotion(ctx, mutation); err == nil {
		t.Fatal("CommitBacklogPromotion(injected fault) error = nil")
	}
	stillReady, err := store.GetBacklogItem(ctx, target.Handle)
	if err != nil || stillReady.Readiness != domain.BacklogReady {
		t.Fatalf("backlog after rolled-back finalization = %#v, %v", stillReady, err)
	}
	if _, err := store.GetOperation(ctx, reservation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("promotion operation after rollback error = %v, want ErrNotFound", err)
	}
	if _, err := store.db.ExecContext(ctx, "DROP TRIGGER refuse_backlog_promotion_operation"); err != nil {
		t.Fatal(err)
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
	assertReplayFails := func(label string) {
		t.Helper()
		if _, found, err := reopened.ReplayBacklogPromotion(
			ctx, reservation.OperationID, reservation.SubjectDigest,
		); err == nil || found {
			t.Fatalf("ReplayBacklogPromotion(%s) = found %t, error %v", label, found, err)
		}
	}
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE backlog_promotions SET subject_digest = ? WHERE operation_id = ?",
		strings.Repeat("d", 64), reservation.OperationID,
	); err != nil {
		t.Fatal(err)
	}
	assertReplayFails("altered durable digest")
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE backlog_promotions SET subject_digest = ? WHERE operation_id = ?",
		reservation.SubjectDigest, reservation.OperationID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE backlog_items SET readiness = ? WHERE handle = ?", domain.BacklogReady, target.Handle,
	); err != nil {
		t.Fatal(err)
	}
	assertReplayFails("demoted durable item")
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE backlog_items SET readiness = ? WHERE handle = ?", domain.BacklogPromoted, target.Handle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE operations SET result_ref = ? WHERE id = ?", "task-substituted", reservation.TaskOperationID,
	); err != nil {
		t.Fatal(err)
	}
	assertReplayFails("altered child operation")
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE backlog_promotions SET completed_at = ? WHERE operation_id = ?", "invalid-time", reservation.OperationID,
	); err != nil {
		t.Fatal(err)
	}
	assertReplayFails("invalid completion time")
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE backlog_promotions SET reserved_at = ? WHERE operation_id = ?", "invalid-time", reservation.OperationID,
	); err != nil {
		t.Fatal(err)
	}
	assertReplayFails("invalid reservation time")
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

func TestBacklogPromotionRequiresExactReservationAndDurablePreparation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	target := backlogAdditionItem("backlog-exact-promotion", nil)
	if err := store.CreateBacklogItem(ctx, target); err != nil {
		t.Fatal(err)
	}
	reservation := application.BacklogPromotionReservation{
		OperationID: "operation-promote-exact", SubjectDigest: strings.Repeat("e", 64),
		BacklogHandle: target.Handle, TaskOperationID: "backlog-task-exact",
		ReservedAt: backlogPromotionTime(),
	}
	if replay, found, err := store.ReplayBacklogPromotion(
		ctx, reservation.OperationID, reservation.SubjectDigest,
	); err != nil || found || replay.Operation.ID != "" {
		t.Fatalf("ReplayBacklogPromotion(missing) = %#v, %t, %v", replay, found, err)
	}
	invalidReservation := reservation
	invalidReservation.Item = target
	if _, err := store.ReserveBacklogPromotion(ctx, invalidReservation); err == nil {
		t.Fatal("ReserveBacklogPromotion(caller item) error = nil")
	}
	reserved, err := store.ReserveBacklogPromotion(ctx, reservation)
	if err != nil {
		t.Fatal(err)
	}
	alteredReplay := reservation
	alteredReplay.SubjectDigest = strings.Repeat("f", 64)
	if _, err := store.ReserveBacklogPromotion(ctx, alteredReplay); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReserveBacklogPromotion(altered replay) error = %v, want ErrConflict", err)
	}

	mutations := sqliteMutations(t, store, &sequenceIDs{ids: []string{"task-unrelated", "task-exact"}}, backlogPromotionTime())
	unrelatedCommand := sqlitePrepareCommand()
	unrelatedCommand.OperationID = "operation-unrelated-task"
	unrelatedCommand.RepositoryID = target.RepositoryID
	unrelatedCommand.Shape = target.Shape
	unrelated, err := mutations.PrepareTask(ctx, unrelatedCommand)
	if err != nil {
		t.Fatal(err)
	}
	unrelated.Operation.ID = reservation.TaskOperationID
	if _, err := store.CommitBacklogPromotion(ctx, application.BacklogPromotionMutation{
		Reservation: reserved, Prepared: unrelated, At: backlogPromotionTime().Add(time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitBacklogPromotion(missing child) error = %v, want ErrPrecondition", err)
	}
	prepare := sqlitePrepareCommand()
	prepare.OperationID = reservation.TaskOperationID
	prepare.RepositoryID = target.RepositoryID
	prepare.Shape = target.Shape
	prepared, err := mutations.PrepareTask(ctx, prepare)
	if err != nil {
		t.Fatal(err)
	}
	alteredPrepared := prepared
	alteredPrepared.Task.RepositoryID = "repository-altered"
	if _, err := store.CommitBacklogPromotion(ctx, application.BacklogPromotionMutation{
		Reservation: reserved, Prepared: alteredPrepared, At: backlogPromotionTime().Add(time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitBacklogPromotion(altered child) error = %v, want ErrPrecondition", err)
	}
	alteredReservation := reserved
	alteredReservation.Item.RequestedOutcome = "Altered after reservation."
	if _, err := store.CommitBacklogPromotion(ctx, application.BacklogPromotionMutation{
		Reservation: alteredReservation, Prepared: prepared, At: backlogPromotionTime().Add(time.Minute),
	}); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitBacklogPromotion(altered reservation) error = %v, want ErrPrecondition", err)
	}
}

func backlogPromotionTime() time.Time {
	return time.Date(2026, time.August, 20, 22, 0, 0, 0, time.UTC)
}
