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

func steerMutation(taskHandle, operationID, instruction string, at time.Time) application.TaskSteerMutation {
	return application.TaskSteerMutation{
		TaskHandle: taskHandle, OperationID: operationID, Instruction: instruction,
		SubjectDigest: strings.Repeat("7", 64), At: at,
	}
}

// An instruction reaches the worker exactly once. Delivering it inside the same
// transaction that records the report is what makes that true: a worker that
// read the same instruction on two reports would act on it twice, and an
// operator who said "stop touching the parser" once would have said it twice.
func TestStore_ASteeringInstructionReachesTheWorkerExactlyOnce(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	if _, err := store.CommitTaskSteer(context.Background(),
		steerMutation(task.Handle, "operation-steer-0001", "Prefer the existing parser.", at)); err != nil {
		t.Fatalf("CommitTaskSteer() error = %v", err)
	}

	first, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0001", domain.ReportProgress), at.Add(time.Minute)))
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if first.Instruction != "Prefer the existing parser." {
		t.Fatalf("first receipt instruction = %q", first.Instruction)
	}

	second, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0002", domain.ReportProgress), at.Add(2*time.Minute)))
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if second.Instruction != "" {
		t.Errorf("an instruction already read was delivered again: %q", second.Instruction)
	}
}

// Two instructions are two things the operator said. Keeping only the newest
// would silently drop the first, and nothing would tell the operator it never
// arrived.
func TestStore_SteeringInstructionsQueueInOrderRatherThanOverwrite(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	for index, instruction := range []string{"First instruction.", "Second instruction."} {
		if _, err := store.CommitTaskSteer(context.Background(), steerMutation(
			task.Handle, "operation-steer-000"+string(rune('1'+index)), instruction, at,
		)); err != nil {
			t.Fatalf("CommitTaskSteer(%d) error = %v", index, err)
		}
	}

	pending, err := store.PendingSteeringInstructions(context.Background(), task.Handle)
	if err != nil || pending != 2 {
		t.Fatalf("PendingSteeringInstructions() = %d, %v, want 2", pending, err)
	}
	first, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0001", domain.ReportProgress), at.Add(time.Minute)))
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if first.Instruction != "First instruction." {
		t.Errorf("instructions must arrive in the order they were sent, got %q", first.Instruction)
	}
	second, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0002", domain.ReportProgress), at.Add(2*time.Minute)))
	if err != nil {
		t.Fatalf("CommitReport() error = %v", err)
	}
	if second.Instruction != "Second instruction." {
		t.Errorf("second receipt instruction = %q", second.Instruction)
	}
}

// An instruction for a task with no worker can never be read. Queuing it would
// leave an operator believing they had said something to someone.
func TestStore_SteerRefusesATaskWithNoWorker(t *testing.T) {
	store, _ := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	unlaunched := storeTask("task-unlaunched-steer", 1)
	if err := store.CreateTask(context.Background(), unlaunched); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}

	_, err := store.CommitTaskSteer(context.Background(), steerMutation(
		unlaunched.Handle, "operation-steer-0001", "Do the thing.",
		time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)))

	if !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitTaskSteer(unlaunched) error = %v, want a precondition refusal", err)
	}
}

// An instruction carrying control characters could smuggle terminal escape
// sequences into whatever renders it. It is text a human wrote, not a command
// sequence, and the boundary refuses it rather than storing it.
func TestStore_SteerRefusesAnInstructionThatIsNotPlainText(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	for name, instruction := range map[string]string{
		"empty":            "",
		"escape sequence":  "clear \x1b[2J the screen",
		"newline":          "first line\nsecond line",
		"null byte":        "before\x00after",
		"beyond the bound": strings.Repeat("x", 2049),
	} {
		if _, err := store.CommitTaskSteer(context.Background(),
			steerMutation(task.Handle, "operation-steer-"+strings.ReplaceAll(name, " ", "-"), instruction, at),
		); !errors.Is(err, application.ErrPrecondition) {
			t.Errorf("%s: CommitTaskSteer() error = %v, want a precondition refusal", name, err)
		}
	}
}

// Steering says something to a worker that is still working. Moving the task
// would claim the instruction had an effect nobody has observed yet.
func TestStore_SteerDoesNotMoveTheTask(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)

	result, err := store.CommitTaskSteer(context.Background(),
		steerMutation(task.Handle, "operation-steer-0001", "Prefer the existing parser.", at))
	if err != nil {
		t.Fatalf("CommitTaskSteer() error = %v", err)
	}
	if result.Task.State != task.State {
		t.Errorf("steering moved the task from %q to %q", task.State, result.Task.State)
	}
}

func TestStore_ARepeatedSteerReplaysRatherThanQueueingTwice(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	mutation := steerMutation(task.Handle, "operation-steer-0001", "Prefer the existing parser.", at)

	if _, err := store.CommitTaskSteer(context.Background(), mutation); err != nil {
		t.Fatalf("CommitTaskSteer() error = %v", err)
	}
	if _, err := store.CommitTaskSteer(context.Background(), mutation); err != nil {
		t.Fatalf("CommitTaskSteer(replay) error = %v", err)
	}

	pending, err := store.PendingSteeringInstructions(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("PendingSteeringInstructions() error = %v", err)
	}
	if pending != 1 {
		t.Errorf("pending instructions = %d, want 1: a repeat must not say it twice", pending)
	}
}

func TestStore_SteeringReadsRefuseAnAbsentCaller(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))

	if _, err := store.PendingSteeringInstructions(nilPromotionContext(), task.Handle); err == nil {
		t.Error("PendingSteeringInstructions(nil) error = nil")
	}
}

// A storage failure is reported, never swallowed into "no instruction".
func TestStore_SteeringReportsStorageFailures(t *testing.T) {
	store, task := openReportFixture(t, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	at := time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC)
	if _, err := store.db.Exec(
		"ALTER TABLE task_steering_instructions RENAME TO unavailable_task_steering"); err != nil {
		t.Fatalf("break steering table: %v", err)
	}

	if _, err := store.CommitTaskSteer(context.Background(),
		steerMutation(task.Handle, "operation-steer-0001", "Do the thing.", at)); err == nil {
		t.Error("CommitTaskSteer() with an unwritable table error = nil")
	}
	if _, err := store.PendingSteeringInstructions(context.Background(), task.Handle); err == nil {
		t.Error("PendingSteeringInstructions() with an unreadable table error = nil")
	}
	if _, err := store.CommitReport(context.Background(),
		directReportMutation(task, sqliteWorkerReport(task, "report-0001", domain.ReportProgress), at.Add(time.Minute)),
	); err == nil {
		t.Error("CommitReport() with an unreadable steering table error = nil")
	}
}
