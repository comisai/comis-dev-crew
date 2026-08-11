package validation

import (
	"testing"
	"time"
)

func TestProcessRecord_ValidatesClosedLifecycleAndMonotonicIdentity(t *testing.T) {
	startedAt := time.Date(2026, time.August, 11, 15, 0, 0, 0, time.UTC)
	starting := ProcessRecord{
		TaskHandle: "task-alpha", OperationID: "validate-alpha", ProgramID: "go-test",
		ExecutableLabel: "go-test", State: ProcessStarting,
		StartedAt: startedAt, ObservedAt: startedAt,
	}
	running := starting
	running.PID = 4321
	running.StartIdentity = "start-identity-4321"
	running.ProcessGroupIdentity = "4321"
	running.State = ProcessRunning
	running.ObservedAt = startedAt.Add(time.Second)
	exitCode := 0
	exited := running
	exited.State = ProcessExited
	exited.ObservedAt = startedAt.Add(2 * time.Second)
	exited.ExitCode = &exitCode
	unknown := running
	unknown.State = ProcessUnknown
	unknown.ObservedAt = startedAt.Add(2 * time.Second)
	for _, record := range []ProcessRecord{starting, running, exited, unknown} {
		if err := record.Validate(); err != nil {
			t.Fatalf("Validate(%s) error = %v", record.State, err)
		}
	}
	if err := running.CanFollow(starting); err != nil {
		t.Fatalf("running.CanFollow(starting) error = %v", err)
	}
	if err := exited.CanFollow(running); err != nil {
		t.Fatalf("exited.CanFollow(running) error = %v", err)
	}
	if err := unknown.CanFollow(running); err != nil {
		t.Fatalf("unknown.CanFollow(running) error = %v", err)
	}
	if err := running.CanFollow(running); err != nil {
		t.Fatalf("running.CanFollow(replay) error = %v", err)
	}
}

func TestProcessRecord_RejectsIncompleteAndRegressiveEvidence(t *testing.T) {
	startedAt := time.Date(2026, time.August, 11, 15, 0, 0, 0, time.UTC)
	valid := ProcessRecord{
		TaskHandle: "task-alpha", OperationID: "validate-alpha", ProgramID: "go-test",
		ExecutableLabel: "go-test", PID: 4321, StartIdentity: "start-identity-4321",
		ProcessGroupIdentity: "4321", State: ProcessRunning,
		StartedAt: startedAt, ObservedAt: startedAt.Add(time.Second),
	}
	for _, test := range []struct {
		name   string
		mutate func(*ProcessRecord)
	}{
		{name: "invalid task", mutate: func(record *ProcessRecord) { record.TaskHandle = "bad task" }},
		{name: "changed executable label", mutate: func(record *ProcessRecord) { record.ExecutableLabel = "node-test" }},
		{name: "non utc time", mutate: func(record *ProcessRecord) { record.ObservedAt = record.ObservedAt.In(time.FixedZone("other", 0)) }},
		{name: "observation before start", mutate: func(record *ProcessRecord) { record.ObservedAt = startedAt.Add(-time.Second) }},
		{name: "missing pid", mutate: func(record *ProcessRecord) { record.PID = 0 }},
		{name: "running exit", mutate: func(record *ProcessRecord) { value := 1; record.ExitCode = &value }},
		{name: "unknown state", mutate: func(record *ProcessRecord) { record.State = "forged" }},
		{name: "starting with pid", mutate: func(record *ProcessRecord) { record.State = ProcessStarting }},
		{name: "exited without code", mutate: func(record *ProcessRecord) { record.State = ProcessExited }},
		{name: "unknown with code", mutate: func(record *ProcessRecord) { value := 1; record.State = ProcessUnknown; record.ExitCode = &value }},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.mutate(&record)
			if err := record.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
	changed := valid
	changed.StartIdentity = "changed-start-identity"
	if err := changed.CanFollow(valid); err == nil {
		t.Fatal("CanFollow(changed identity) error = nil")
	}
	regressed := valid
	regressed.State = ProcessStarting
	regressed.PID = 0
	regressed.StartIdentity = ""
	regressed.ProcessGroupIdentity = ""
	regressed.ObservedAt = startedAt.Add(2 * time.Second)
	if err := regressed.CanFollow(valid); err == nil {
		t.Fatal("CanFollow(regressed state) error = nil")
	}
	invalidPrevious := valid
	invalidPrevious.ProgramID = "bad program"
	if err := valid.CanFollow(invalidPrevious); err == nil {
		t.Fatal("CanFollow(invalid previous) error = nil")
	}
}
