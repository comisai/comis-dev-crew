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

func TestBacklogAdditionCommitsItemAndOperationAcrossRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	dependency := backlogAdditionItem("backlog-dependency", nil)
	dependency.Readiness = domain.BacklogPromoted
	if err := store.CreateBacklogItem(ctx, dependency); err != nil {
		t.Fatal(err)
	}
	mutation := application.BacklogAdditionMutation{
		OperationID: "operation-add-backlog", SubjectDigest: strings.Repeat("a", 64),
		Item: backlogAdditionItem("backlog-added", []string{dependency.Handle}),
		At:   backlogAdditionTime(),
	}
	if _, found, err := store.ReplayBacklogAddition(ctx, mutation.OperationID, mutation.SubjectDigest); err != nil || found {
		t.Fatalf("ReplayBacklogAddition(before) found/error = %t/%v", found, err)
	}
	committed, err := store.CommitBacklogAddition(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitBacklogAddition() error = %v", err)
	}
	if !reflect.DeepEqual(committed.Item, mutation.Item) || committed.Operation.Command != "AddBacklog" ||
		committed.Operation.ResultRef != mutation.Item.Handle || committed.Operation.StateVersion < 1 {
		t.Fatalf("CommitBacklogAddition() = %#v", committed)
	}
	replayed, err := store.CommitBacklogAddition(ctx, mutation)
	if err != nil || !reflect.DeepEqual(replayed, committed) {
		t.Fatalf("CommitBacklogAddition(replay) = %#v, %v, want %#v", replayed, err, committed)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, found, err := reopened.ReplayBacklogAddition(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(restarted, committed) {
		t.Fatalf("ReplayBacklogAddition(restart) = %#v, %t, %v", restarted, found, err)
	}
	if _, _, err := reopened.ReplayBacklogAddition(
		ctx, mutation.OperationID, strings.Repeat("b", 64),
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayBacklogAddition(altered) error = %v, want ErrConflict", err)
	}
}

func TestBacklogAdditionRejectsMissingDependencyWithoutPartialCommit(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := application.BacklogAdditionMutation{
		OperationID: "operation-add-missing-dependency", SubjectDigest: strings.Repeat("c", 64),
		Item: backlogAdditionItem("backlog-refused", []string{"backlog-missing"}),
		At:   backlogAdditionTime(),
	}
	if _, err := store.CommitBacklogAddition(ctx, mutation); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitBacklogAddition(missing dependency) error = %v, want ErrPrecondition", err)
	}
	if _, err := store.GetBacklogItem(ctx, mutation.Item.Handle); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetBacklogItem(refused) error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetOperation(refused) error = %v, want ErrNotFound", err)
	}
}

func TestBacklogAdditionRollsBackItemWhenOperationPersistenceFails(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_backlog_addition_operation
		BEFORE INSERT ON operations WHEN NEW.command = 'AddBacklog'
		BEGIN SELECT RAISE(ABORT, 'injected backlog operation failure'); END`); err != nil {
		t.Fatal(err)
	}
	mutation := application.BacklogAdditionMutation{
		OperationID: "operation-add-persistence-fault", SubjectDigest: strings.Repeat("d", 64),
		Item: backlogAdditionItem("backlog-persistence-fault", nil), At: backlogAdditionTime(),
	}
	if _, err := store.CommitBacklogAddition(ctx, mutation); err == nil {
		t.Fatal("CommitBacklogAddition(injected fault) error = nil")
	}
	if _, err := store.GetBacklogItem(ctx, mutation.Item.Handle); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetBacklogItem(rolled back) error = %v, want ErrNotFound", err)
	}
	if _, err := store.GetOperation(ctx, mutation.OperationID); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetOperation(rolled back) error = %v, want ErrNotFound", err)
	}
}

func backlogAdditionItem(handle string, dependencies []string) domain.BacklogItem {
	return domain.BacklogItem{
		SchemaVersion: 1, Handle: handle, RepositoryID: "repo-primary", Shape: domain.ShapeShip,
		RequestedOutcome: "Implement the bounded request.", DependsOn: dependencies,
		Priority: domain.BacklogPriorityNormal, Readiness: domain.BacklogReady,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		CreatedAt:             backlogAdditionTime(), UpdatedAt: backlogAdditionTime(),
	}
}

func backlogAdditionTime() time.Time {
	return time.Date(2026, time.August, 20, 21, 0, 0, 0, time.UTC)
}
