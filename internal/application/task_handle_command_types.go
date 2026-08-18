package application

import "time"

// The commands in this file all name one task by handle and carry nothing else.
// That shape is deliberate and load-bearing: an operator command that could also
// choose a worker, a validation profile, a disposition or an instruction would
// let two different intents travel under one name, and the transcript afterwards
// would not say which was meant.

// PauseTaskCommand asks one task's worker to settle at a safe boundary.
//
// It carries no instruction text and no interrupt. Pause is a request to stop
// cleanly, not a message to the worker and not a signal: an operator who needs
// to say something uses steering, and one who needs to stop work now cancels.
// Keeping those separate is what lets a paused worktree be handed to a
// developer in a known state.
type PauseTaskCommand struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
}

// TaskPauseRequestMutation is the durable half of one pause request.
type TaskPauseRequestMutation struct {
	TaskHandle    string
	OperationID   string
	SubjectDigest string
	At            time.Time
}

// CancelTaskCommand stops one task at an operator's request.
//
// It preserves artifacts. Stopping work and discarding it are separate
// decisions with separate evidence requirements, and a cancel that also removed
// the worktree would make the stop irreversible at the moment an operator is
// least certain.
type CancelTaskCommand struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
}

// TaskCancelMutation is the durable half of one operator cancellation.
type TaskCancelMutation struct {
	TaskHandle    string
	OperationID   string
	SubjectDigest string
	At            time.Time
}

// ResumeTaskCommand returns one paused task to its existing worker.
//
// It carries no instruction and selects no worker: resume continues what was
// already running. Choosing a different worker is replacement, which reconciles
// a fresh brief rather than assuming the old one still describes the tree.
type ResumeTaskCommand struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
}

// TaskResumeMutation is the durable half of one resume, carrying the head the
// caller proved the worktree was sitting at when it checked.
type TaskResumeMutation struct {
	TaskHandle           string
	OperationID          string
	SubjectDigest        string
	ObservedHeadRevision string
	At                   time.Time
}

// VerifyTaskCommand asks the service to validate one task now.
//
// It selects no profile and no checks: validation runs the reviewed profile the
// task was prepared with. A command that could choose its own checks would let a
// caller validate against an easier bar than the one the task was accepted under.
type VerifyTaskCommand struct {
	OperationID string `json:"operationId"`
	TaskHandle  string `json:"taskHandle"`
}

// TaskVerifyMutation is the durable half of one operator-requested validation.
type TaskVerifyMutation struct {
	TaskHandle    string
	OperationID   string
	SubjectDigest string
	At            time.Time
}
