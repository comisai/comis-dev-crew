package application

import "time"

// ReplaceWorkerCommand swaps one paused task's worker and readies it to run
// again from a fresh brief.
//
// It names the task and the replacement profile. It carries no instruction and
// no disposition: replacement preserves the work and changes who continues it,
// and an operator who wants the work stopped or discarded has commands that say
// so plainly.
type ReplaceWorkerCommand struct {
	OperationID     string `json:"operationId"`
	TaskHandle      string `json:"taskHandle"`
	WorkerProfileID string `json:"workerProfileId"`
}

// TaskReplaceMutation is the durable half of one worker swap. It carries the
// workspace snapshot the caller proved, so the recorded trail names the exact
// tree the replacement worker inherits.
type TaskReplaceMutation struct {
	OperationID     string
	SubjectDigest   string
	TaskHandle      string
	WorkerProfileID string
	Snapshot        WorkspaceSnapshot
	At              time.Time
}

// TaskReplacementRecord is the durable trail of one swap: which worker was
// replaced by which, on which tree, under which brief revision.
type TaskReplacementRecord struct {
	OperationID             string
	TaskHandle              string
	PreviousWorkerProfileID string
	WorkerProfileID         string
	HeadRevision            string
	Cleanliness             WorkspaceCleanliness
	BriefRevision           int64
	ObservedAt              time.Time
	StateVersion            int64
}
