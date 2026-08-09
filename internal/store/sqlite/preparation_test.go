package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestMutationReplayConflictIsDurablyAuditedWithoutChangingOriginal(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutations := sqliteMutations(t, store, &sequenceIDs{ids: []string{"task-audit"}},
		time.Date(2026, time.August, 9, 21, 0, 0, 0, time.UTC))
	command := sqlitePrepareCommand()
	if _, err := mutations.PrepareTask(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	altered := command
	altered.AcceptanceCriteria = []string{"This altered subject must be denied."}
	if _, err := mutations.PrepareTask(context.Background(), altered); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("PrepareTask(altered) error = %v, want ErrConflict", err)
	}
	var count int
	if err := store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM operation_replay_conflicts WHERE operation_id = ?`, command.OperationID,
	).Scan(&count); err != nil {
		t.Fatalf("read replay-conflict audit: %v", err)
	}
	if count != 1 {
		t.Fatalf("durable replay-conflict audit count = %d, want 1", count)
	}
}
