package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReadTaskRecoveryEvidence classifies durable unknown-task authority without
// claiming that a workspace is recoverable. Fresh Git truth remains the
// responsibility of the injected reconciliation workspace inspector.
func (store *Store) ReadTaskRecoveryEvidence(
	ctx context.Context,
	taskHandle string,
) (application.TaskRecoveryEvidence, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateTaskHandle(taskHandle) != nil {
		return application.TaskRecoveryEvidence{}, errors.New("read task recovery evidence: input is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return application.TaskRecoveryEvidence{}, fmt.Errorf("begin task recovery evidence read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	complete := func(evidence application.TaskRecoveryEvidence) (application.TaskRecoveryEvidence, error) {
		if err := transaction.Commit(); err != nil {
			return application.TaskRecoveryEvidence{}, fmt.Errorf("commit task recovery evidence read: %w", err)
		}
		return evidence, nil
	}
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return application.TaskRecoveryEvidence{}, err
	}
	if task.State != domain.TaskUnknown {
		return application.TaskRecoveryEvidence{}, fmt.Errorf("task recovery evidence posture: %w", application.ErrPrecondition)
	}
	refused, err := runtimeRelayIdentityRefusalExists(ctx, transaction, taskHandle)
	if err != nil {
		return application.TaskRecoveryEvidence{}, err
	}
	if refused {
		return complete(application.TaskRecoveryEvidence{Kind: application.RecoveryRuntimeRelayIdentityUnproven})
	}
	var candidateReports int
	if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM reports
		WHERE task_handle = ? AND kind = 'candidate_complete'`, taskHandle).Scan(&candidateReports); err != nil {
		return application.TaskRecoveryEvidence{}, fmt.Errorf("inspect task recovery candidate reports: %w", err)
	}
	recoveryHistory, err := candidateRecoveryHistoryExists(ctx, transaction, taskHandle)
	if err != nil {
		return application.TaskRecoveryEvidence{}, err
	}
	binding, found, err := findTerminalBinding(ctx, transaction, taskHandle)
	if err != nil {
		return application.TaskRecoveryEvidence{}, err
	}
	settled := found && binding.managedRunID == task.ManagedRunID &&
		binding.workspaceLeaseID == task.WorkspaceLeaseID &&
		(binding.latestTransition == application.TerminalExited || binding.latestTransition == application.TerminalReleased)
	if candidateReports != 0 || recoveryHistory || !settled {
		return complete(application.TaskRecoveryEvidence{Kind: application.RecoveryRestartEvidenceUnresolved})
	}
	authority, err := readTaskReconciliationAuthority(ctx, transaction, taskHandle)
	if err != nil {
		if errors.Is(err, application.ErrNotFound) || errors.Is(err, application.ErrPrecondition) {
			return complete(application.TaskRecoveryEvidence{Kind: application.RecoveryRestartEvidenceUnresolved})
		}
		return application.TaskRecoveryEvidence{}, err
	}
	return complete(application.TaskRecoveryEvidence{
		Kind: application.RecoveryTerminalSettledWithoutCandidate, Authority: authority,
	})
}
