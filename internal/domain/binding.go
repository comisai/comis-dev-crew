package domain

import "time"

// TaskBinding is the exact host-owned run and workspace authority acknowledged
// before a prepared task can become ready.
type TaskBinding struct {
	ManagedRunID     string
	WorkspaceLeaseID string
}

// Validate rejects partial or malformed authority binding.
func (binding TaskBinding) Validate() error {
	if err := validateAuthorityReference("managedRunId", binding.ManagedRunID); err != nil {
		return err
	}
	return validateAuthorityReference("workspaceLeaseId", binding.WorkspaceLeaseID)
}

// AcknowledgeBinding atomically attaches the exact authority references and
// applies the prepared-to-ready transition. Identical replay is a no-op;
// altered replay fails closed.
func (task Task) AcknowledgeBinding(binding TaskBinding, occurredAt time.Time) (Task, error) {
	if err := binding.Validate(); err != nil {
		return task, transitionFailure(task, TransitionBindAcknowledged, "binding identity is invalid")
	}
	if task.State == TaskReady && task.ManagedRunID == binding.ManagedRunID && task.WorkspaceLeaseID == binding.WorkspaceLeaseID {
		return task, nil
	}
	if task.State != TaskPrepared || task.ManagedRunID != "" || task.WorkspaceLeaseID != "" {
		return task, transitionFailure(task, TransitionBindAcknowledged, "task is not an unbound preparation")
	}
	bound := task
	bound.ManagedRunID = binding.ManagedRunID
	bound.WorkspaceLeaseID = binding.WorkspaceLeaseID
	return bound.ApplyTransition(TransitionBindAcknowledged, occurredAt)
}
