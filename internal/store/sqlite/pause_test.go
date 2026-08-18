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

func pauseMutation(taskHandle, operationID string, at time.Time) application.TaskPauseRequestMutation {
	return application.TaskPauseRequestMutation{
		TaskHandle: taskHandle, OperationID: operationID,
		SubjectDigest: strings.Repeat("b", 64), At: at,
	}
}

// The whole point of a pause request is that the worker finds out. It rides the
// worker's own report receipt rather than a keystroke path: a request the worker
// pulls at a moment it chose cannot land mid-edit, and needs no terminal to
// deliver. If the receipt did not carry it, the request would sit in the
// database unread and the operator's pause would silently do nothing.
func TestStore_APauseRequestReachesTheWorkerThroughItsNextReceipt(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	before, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0001", domain.ReportProgress), at))
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if before.PauseRequested {
		t.Fatal("no pause was requested, so no receipt may claim one")
	}

	if _, err := store.CommitTaskPauseRequest(context.Background(),
		pauseMutation(task.Handle, "operation-pause-0001", at.Add(time.Minute))); err != nil {
		t.Fatalf("CommitTaskPauseRequest() error = %v", err)
	}

	during, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0002", domain.ReportProgress), at.Add(2*time.Minute)))
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if !during.PauseRequested {
		t.Fatal("a standing pause request must reach the worker on its next receipt")
	}
}

// The paused report is the worker's answer. Honouring it clears the request in
// the same transaction that records the report: a request left standing would
// ask an already-settled worker to settle again, and after a resume would
// immediately re-pause it.
func TestStore_APausedReportClearsTheRequestItAnswered(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	if _, err := store.CommitTaskPauseRequest(context.Background(),
		pauseMutation(task.Handle, "operation-pause-0001", at)); err != nil {
		t.Fatalf("CommitTaskPauseRequest() error = %v", err)
	}

	answered, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-paused-0001", domain.ReportPaused), at.Add(time.Minute)))
	if err != nil {
		t.Fatalf("CommitReport(paused) error = %v", err)
	}
	if answered.PauseRequested {
		t.Error("the report that answers a pause must not also carry the request")
	}

	standing, err := store.PauseRequest(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("PauseRequest() error = %v", err)
	}
	if standing {
		t.Error("an answered pause request must not remain standing")
	}
}

// Asking a settled task to settle cannot be honoured: no worker remains to reach
// a boundary, and a request nothing will ever clear would read forever as a
// pause that is still pending.
func TestStore_RefusesAPauseRequestForATaskWithNoWorker(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	// A prepared task has been created but never launched, so nothing is holding
	// its worktree and nothing could reach a boundary.
	unlaunched := storeTask("task-unlaunched-0001", 1)
	if err := store.CreateTask(context.Background(), unlaunched); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	_, err := store.CommitTaskPauseRequest(context.Background(),
		pauseMutation(unlaunched.Handle, "operation-pause-0001", at))
	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskPauseRequest(settled) error = %v, want a precondition refusal", err)
	}
}

// A repeated pause is ordinary: an operator retries, or two ask at once. The
// second must replay the first rather than record a competing request.
func TestStore_ARepeatedPauseRequestReplays(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	mutation := pauseMutation(task.Handle, "operation-pause-0001", at)

	first, err := store.CommitTaskPauseRequest(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskPauseRequest() error = %v", err)
	}
	second, err := store.CommitTaskPauseRequest(context.Background(), mutation)
	if err != nil {
		t.Fatalf("CommitTaskPauseRequest(replay) error = %v", err)
	}
	if first.Operation.ID != second.Operation.ID ||
		first.Task.StateVersion != second.Task.StateVersion {
		t.Errorf("replayed pause = %+v, want the first result %+v", second.Operation, first.Operation)
	}
}
