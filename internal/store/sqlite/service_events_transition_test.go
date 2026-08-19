package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The stream records transitions, so a write that leaves the state where it was
// records nothing.
//
// A candidate that cannot yet be judged is re-examined on every supervisor pass,
// and each pass rewrites the task to refresh its liveness. If each of those
// rewrites also appended an event, an operator watching a task wait would see a
// steady run of "state changed" lines reporting that nothing changed — and on a
// bounded page, that run pushes every real transition out of view.
func TestServiceEvents_RecordNothingWhenTheStateDidNotChange(t *testing.T) {
	store, task := attestationFixture(t, domain.ShapeScout)

	before, err := store.ReadServiceEvents(context.Background(), 0, 200, task.Handle)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}

	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()

	refreshed := task
	refreshed.StateVersion = task.StateVersion + 1
	refreshed.UpdatedAt = task.UpdatedAt.Add(time.Second)
	if err := updateTaskState(context.Background(), transaction, refreshed); err != nil {
		t.Fatalf("updateTaskState(same state) error = %v", err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}

	after, err := store.ReadServiceEvents(context.Background(), 0, 200, task.Handle)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("a rewrite that changed no state appended %d event(s)", len(after)-len(before))
	}

	// A real transition still records exactly one, in the same transaction as
	// the state it describes.
	moved, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = moved.Rollback() }()
	advanced := refreshed
	advanced.State = domain.TaskCancelled
	advanced.StateVersion = refreshed.StateVersion + 1
	advanced.UpdatedAt = refreshed.UpdatedAt.Add(time.Second)
	if err := updateTaskState(context.Background(), moved, advanced); err != nil {
		t.Fatalf("updateTaskState(new state) error = %v", err)
	}
	if err := moved.Commit(); err != nil {
		t.Fatal(err)
	}

	final, err := store.ReadServiceEvents(context.Background(), 0, 200, task.Handle)
	if err != nil {
		t.Fatalf("ReadServiceEvents() error = %v", err)
	}
	if len(final) != len(before)+1 {
		t.Fatalf("a real transition recorded %d event(s), want exactly one", len(final)-len(before))
	}
	if final[len(final)-1].State != domain.TaskCancelled {
		t.Fatalf("recorded transition = %#v", final[len(final)-1])
	}
}
