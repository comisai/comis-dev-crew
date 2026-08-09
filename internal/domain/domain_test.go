package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestFailure_NewFailureRequiresClosedCodeAndSafeOperatorText(t *testing.T) {
	tests := []struct {
		name    string
		code    ErrorCode
		message string
		hint    string
		wantErr bool
	}{
		{name: "valid conflict", code: ErrorConflict, message: "task already exists", hint: "inspect the existing task", wantErr: false},
		{name: "unknown code", code: ErrorCode("surprise"), message: "bad", hint: "repair", wantErr: true},
		{name: "empty safe message", code: ErrorInternal, message: "", hint: "inspect service health", wantErr: true},
		{name: "empty operator hint", code: ErrorInternal, message: "failed", hint: "", wantErr: true},
		{name: "oversized safe message", code: ErrorInternal, message: strings.Repeat("x", 513), hint: "inspect", wantErr: true},
		{name: "oversized hint", code: ErrorInternal, message: "failed", hint: strings.Repeat("x", 513), wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure, err := NewFailure(test.code, false, test.message, test.hint, nil)
			if test.wantErr && err == nil {
				t.Fatal("NewFailure() error = nil, want validation failure")
			}
			if !test.wantErr && err != nil {
				t.Fatalf("NewFailure() error = %v, want nil", err)
			}
			if !test.wantErr && failure.Code != test.code {
				t.Fatalf("failure code = %q, want %q", failure.Code, test.code)
			}
		})
	}
}

func TestFailure_ErrorTextExcludesWrappedSensitiveCause(t *testing.T) {
	cause := errors.New("credential-shaped private adapter detail")
	failure, err := NewFailure(ErrorUnavailable, true, "forge unavailable", "retry after checking forge health", cause)
	if err != nil {
		t.Fatalf("NewFailure() error = %v", err)
	}
	if strings.Contains(failure.Error(), cause.Error()) {
		t.Fatalf("safe error text leaked wrapped cause: %q", failure.Error())
	}
	if !errors.Is(failure, cause) {
		t.Fatal("failure must preserve its cause for internal errors.Is checks")
	}
}

func TestTaskValidate_AcceptsE0ShipAndScoutRecords(t *testing.T) {
	tests := []Task{
		validTask(ShapeShip, DeliveryPullRequest),
		validTask(ShapeScout, DeliveryReport),
	}
	for _, task := range tests {
		if err := task.Validate(); err != nil {
			t.Fatalf("Task.Validate() error = %v for shape %q", err, task.Shape)
		}
	}
}

func TestTaskValidate_RejectsInvalidOrOutOfScopeTaskFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Task)
	}{
		{name: "schema version", mutate: func(task *Task) { task.SchemaVersion = 2 }},
		{name: "opaque handle", mutate: func(task *Task) { task.Handle = "../escape" }},
		{name: "unknown state", mutate: func(task *Task) { task.State = TaskState("finished") }},
		{name: "unknown shape", mutate: func(task *Task) { task.Shape = TaskShape("initiative") }},
		{name: "repository id", mutate: func(task *Task) { task.RepositoryID = "owner/repo" }},
		{name: "base revision", mutate: func(task *Task) { task.BaseRevision = "main" }},
		{name: "brief revision", mutate: func(task *Task) { task.BriefRevision = 0 }},
		{name: "unknown delivery", mutate: func(task *Task) { task.DeliveryMode = DeliveryMode("local_branch") }},
		{name: "ship report mismatch", mutate: func(task *Task) { task.DeliveryMode = DeliveryReport }},
		{name: "scout pull request mismatch", mutate: func(task *Task) { task.Shape = ShapeScout }},
		{name: "negative report cursor", mutate: func(task *Task) { task.ReportCursor = -1 }},
		{name: "state version", mutate: func(task *Task) { task.StateVersion = 0 }},
		{name: "missing timestamp", mutate: func(task *Task) { task.CreatedAt = time.Time{} }},
		{name: "non UTC timestamp", mutate: func(task *Task) { task.UpdatedAt = task.UpdatedAt.In(time.FixedZone("offset", 3600)) }},
		{name: "time order", mutate: func(task *Task) { task.UpdatedAt = task.CreatedAt.Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			task := validTask(ShapeShip, DeliveryPullRequest)
			test.mutate(&task)
			if err := task.Validate(); err == nil {
				t.Fatal("Task.Validate() error = nil, want validation failure")
			}
		})
	}
}

func TestOpaqueReferenceValidators_RejectUntrustedReferences(t *testing.T) {
	if err := ValidateTaskHandle("task-0001"); err != nil {
		t.Fatalf("ValidateTaskHandle(valid) error = %v", err)
	}
	if err := ValidateTaskHandle("../escape"); err == nil {
		t.Fatal("ValidateTaskHandle(escape) error = nil")
	}
	if err := ValidateOperationID("op-0001"); err != nil {
		t.Fatalf("ValidateOperationID(valid) error = %v", err)
	}
	if err := ValidateOperationID("bad id"); err == nil {
		t.Fatal("ValidateOperationID(invalid) error = nil")
	}
}

func TestOperationValidate_EnforcesReplayAndOutcomeInvariants(t *testing.T) {
	valid := validOperation()
	if err := valid.Validate(); err != nil {
		t.Fatalf("OperationRecord.Validate() error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*OperationRecord)
	}{
		{name: "schema version", mutate: func(operation *OperationRecord) { operation.SchemaVersion = 2 }},
		{name: "operation id", mutate: func(operation *OperationRecord) { operation.ID = "bad id" }},
		{name: "command", mutate: func(operation *OperationRecord) { operation.Command = "" }},
		{name: "subject digest", mutate: func(operation *OperationRecord) { operation.SubjectDigest = "abcd" }},
		{name: "unknown status", mutate: func(operation *OperationRecord) { operation.Status = OperationStatus("done") }},
		{name: "rejected without code", mutate: func(operation *OperationRecord) { operation.Status = OperationRejected }},
		{name: "completed with error", mutate: func(operation *OperationRecord) { operation.ErrorCode = ErrorConflict }},
		{name: "zero state version", mutate: func(operation *OperationRecord) { operation.StateVersion = 0 }},
		{name: "time order", mutate: func(operation *OperationRecord) { operation.UpdatedAt = operation.CreatedAt.Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			operation := validOperation()
			test.mutate(&operation)
			if err := operation.Validate(); err == nil {
				t.Fatal("OperationRecord.Validate() error = nil, want validation failure")
			}
		})
	}
}

func validTask(shape TaskShape, delivery DeliveryMode) Task {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	task := Task{
		SchemaVersion:      1,
		Handle:             "task-0001",
		State:              TaskPrepared,
		Shape:              shape,
		RepositoryID:       "product-api",
		BaseRevision:       strings.Repeat("a", 40),
		BriefRevision:      1,
		AcceptanceCriteria: []string{"The requested behavior is proven."},
		Constraints:        []string{"Preserve unrelated work."},
		ValidationProfile:  "go-default",
		DeliveryMode:       delivery,
		WorkerProfileID:    "codex-standard",
		ReportCursor:       0,
		StateVersion:       1,
		CreatedAt:          created,
		UpdatedAt:          created,
	}
	pinned, err := task.PinBriefRevision()
	if err != nil {
		panic(err)
	}
	return pinned
}

func validOperation() OperationRecord {
	created := time.Date(2026, time.August, 8, 20, 0, 0, 0, time.UTC)
	return OperationRecord{
		SchemaVersion: 1,
		ID:            "op-0001",
		Command:       "GetTask",
		SubjectDigest: strings.Repeat("b", 64),
		Status:        OperationCompleted,
		StateVersion:  1,
		CreatedAt:     created,
		UpdatedAt:     created,
	}
}
