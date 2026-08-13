package application

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestQueries_ExplainUnknownTaskUsesReasonSpecificRecoveryEvidence(t *testing.T) {
	unknown := queryTask("task-unknown-explain", domain.TaskUnknown, 11)
	unknown.ManagedRunID = "managed-run-unknown"
	unknown.WorkspaceLeaseID = "workspace-lease-unknown"
	unknown.ExecutionAttachmentID = "execution-attachment-unknown"
	unknown.AttachmentTargetName = "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock"
	authority := TaskReconciliationAuthority{
		Task: unknown,
		Preparation: ManagedRunPreparation{
			ExternalRunRef: unknown.Handle, RequestedWorkspaceRoot: "/approved/worktrees/" + unknown.Handle,
			State: PreparationOpen,
		},
		PreparationOperationID: "operation-prepare-unknown",
		TerminalSessionID:      "terminal-session-unknown",
		TerminalTransition:     TerminalExited,
		TerminalObservedAt:     unknown.UpdatedAt,
	}
	recoverable := TaskRecoveryEvidence{Kind: RecoveryTerminalSettledWithoutCandidate, Authority: authority}
	tests := []struct {
		name       string
		task       domain.Task
		host       HostIntegrationStatus
		recovery   TaskRecoveryEvidence
		inspector  *queryReconciliationInspector
		wantCode   string
		wantAction NextAction
	}{
		{name: "terminal exited without candidate", task: unknown, host: queryHostStatus(true), recovery: recoverable,
			inspector: &queryReconciliationInspector{snapshot: WorkspaceSnapshot{
				TaskHandle: unknown.Handle, RepositoryID: unknown.RepositoryID,
				WorktreePath: authority.Preparation.RequestedWorkspaceRoot, Branch: "devcrew/task-unknown",
				HeadRevision: strings.Repeat("b", 40), Cleanliness: WorkspaceClean,
			}}, wantCode: "terminal_exited_without_candidate_evidence", wantAction: ActionReconcileTask},
		{name: "restart evidence unresolved", task: unknown, host: queryHostStatus(true),
			recovery: TaskRecoveryEvidence{Kind: RecoveryRestartEvidenceUnresolved},
			wantCode: "restart_evidence_unresolved", wantAction: ActionInspectTask},
		{name: "host integration unavailable", task: unknown, host: queryHostStatus(false), recovery: recoverable,
			wantCode: "host_integration_unavailable", wantAction: ActionInspectHealth},
		{name: "workspace not recoverable", task: unknown, host: queryHostStatus(true), recovery: recoverable,
			inspector: &queryReconciliationInspector{err: errors.New("private workspace failure")},
			wantCode:  "workspace_not_recoverable", wantAction: ActionPrepareTask},
		{name: "reconciliation already in progress", task: queryTask("task-reconciling-explain", domain.TaskReconciling, 12), host: queryHostStatus(true),
			wantCode: "reconciliation_in_progress", wantAction: ActionInspectTask},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repository := &queryRecoveryRepository{
				queryRepository: &queryRepository{tasks: []domain.Task{test.task}}, recovery: test.recovery,
			}
			queries, err := NewQueries(QueryConfig{
				Repository: repository, Host: test.host, ReconciliationWorkspaces: test.inspector, Clock: time.Now,
			})
			if err != nil {
				t.Fatal(err)
			}
			explanation, err := queries.ExplainTask(context.Background(), test.task.Handle)
			if err != nil || explanation.ReasonCode != test.wantCode ||
				!slices.Contains(explanation.NextSafeActions, test.wantAction) {
				t.Fatalf("ExplainTask() = %#v, %v", explanation, err)
			}
		})
	}
}

type queryRecoveryRepository struct {
	*queryRepository
	recovery TaskRecoveryEvidence
	err      error
}

func (repository *queryRecoveryRepository) ReadTaskRecoveryEvidence(
	context.Context,
	string,
) (TaskRecoveryEvidence, error) {
	return repository.recovery, repository.err
}

type queryReconciliationInspector struct {
	snapshot WorkspaceSnapshot
	err      error
}

func (inspector *queryReconciliationInspector) InspectReconciliationCandidate(
	context.Context,
	ReconciliationWorkspaceRequest,
) (WorkspaceSnapshot, error) {
	return inspector.snapshot, inspector.err
}
