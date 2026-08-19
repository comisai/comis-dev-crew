package application

import (
	"context"
	"time"
)

// TaskLogSource is the closed set of durable records a task's history is read
// from.
//
// They stay separate because they answer different questions and carry different
// authority: a worker entry is a claim the worker made, a service entry is
// something the service durably did, and a validation entry is what actually
// ran. Blending them would leave an operator unable to tell a claim from a fact,
// which is the distinction the precedence model rests on.
type TaskLogSource string

const (
	// LogSourceWorker is what the worker reported about its own progress.
	LogSourceWorker TaskLogSource = "worker"
	// LogSourceService is what the service durably recorded happening.
	LogSourceService TaskLogSource = "service"
	// LogSourceValidation is which reviewed programs ran and how they ended.
	LogSourceValidation TaskLogSource = "validation"
)

// Valid reports whether the source is one this service can read.
func (source TaskLogSource) Valid() bool {
	switch source {
	case LogSourceWorker, LogSourceService, LogSourceValidation:
		return true
	default:
		return false
	}
}

// TaskLogEntry is one bounded, resumable line of a task's private history.
//
// Detail carries worker-authored text only for the worker source, and that text
// was bounded and rejected for control characters when the report was accepted,
// so it cannot carry a terminal escape sequence into whatever renders it.
type TaskLogEntry struct {
	Sequence   int64         `json:"sequence"`
	OccurredAt time.Time     `json:"occurredAt"`
	Source     TaskLogSource `json:"source"`
	Label      string        `json:"label"`
	Detail     string        `json:"detail,omitempty"`
	Outcome    string        `json:"outcome,omitempty"`
}

// TaskLogPage is one bounded page plus the cursor to resume from.
type TaskLogPage struct {
	SchemaVersion int            `json:"schemaVersion"`
	CapturedAt    time.Time      `json:"capturedAt"`
	TaskHandle    string         `json:"taskHandle"`
	Source        TaskLogSource  `json:"source"`
	NextCursor    int64          `json:"nextCursor"`
	Entries       []TaskLogEntry `json:"entries"`
}

// TaskLogStore reads one task's history from one durable source.
type TaskLogStore interface {
	ReadTaskLogs(context.Context, string, TaskLogSource, int64, int) ([]TaskLogEntry, error)
}
