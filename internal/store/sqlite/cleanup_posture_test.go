package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestTaskCleanupStore_ClassifiesActiveAndUnknownTaskPosture(t *testing.T) {
	tests := []struct {
		state string
		want  error
	}{
		{state: "working", want: application.ErrCleanupActiveExecution},
		{state: "unknown", want: application.ErrCleanupUnknownExecution},
	}
	for _, test := range tests {
		t.Run(test.state, func(t *testing.T) {
			store, task, _ := deliveredCleanupFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
			t.Cleanup(func() { _ = store.Close() })
			if _, err := store.db.Exec("UPDATE tasks SET state = ? WHERE handle = ?", test.state, task.Handle); err != nil {
				t.Fatalf("seed task state: %v", err)
			}
			at := task.UpdatedAt.Add(time.Minute)
			_, err := store.BeginTaskCleanup(context.Background(), application.TaskCleanupMutation{
				OperationID: "cleanup-posture-0001", SubjectDigest: strings.Repeat("e", 64),
				TaskHandle: task.Handle, ReleaseOperationID: "release-posture-0001", ReleasedAt: at, At: at,
			})
			if !errors.Is(err, application.ErrPrecondition) || !errors.Is(err, test.want) {
				t.Fatalf("BeginTaskCleanup(%s) error = %v, want %v", test.state, err, test.want)
			}
		})
	}
}
