package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReadTaskEvidence returns one content-free diagnostic snapshot from a single
// durable read transaction. It never treats missing evidence as success.
func (store *Store) ReadTaskEvidence(ctx context.Context, taskHandle string) (application.TaskEvidenceView, error) {
	observation, err := store.ReadTaskObservation(ctx, taskHandle)
	return observation.Evidence, err
}

// ReadTaskObservation joins one task and its evidence in a single transaction.
func (store *Store) ReadTaskObservation(ctx context.Context, taskHandle string) (application.TaskObservation, error) {
	if store == nil || store.db == nil || ctx == nil || domain.ValidateTaskHandle(taskHandle) != nil {
		return application.TaskObservation{}, errors.New("read task observation: input is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return application.TaskObservation{}, fmt.Errorf("begin task observation read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	task, err := getTask(ctx, transaction, taskHandle)
	if err != nil {
		return application.TaskObservation{}, err
	}
	evidence, err := readTaskEvidence(ctx, transaction, task)
	if err != nil {
		return application.TaskObservation{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.TaskObservation{}, fmt.Errorf("commit task observation read: %w", err)
	}
	return application.TaskObservation{Task: task, Evidence: evidence}, nil
}

// TaskEvidenceSnapshot reads all task rows, evidence, and the advertised
// global version without allowing a concurrent mutation to mix projections.
func (store *Store) TaskEvidenceSnapshot(ctx context.Context) ([]application.TaskObservation, int64, error) {
	if store == nil || store.db == nil || ctx == nil {
		return nil, 0, errors.New("read task evidence snapshot: input is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, 0, fmt.Errorf("begin task evidence snapshot: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	tasks, err := listTasks(ctx, transaction)
	if err != nil {
		return nil, 0, err
	}
	observations := make([]application.TaskObservation, 0, len(tasks))
	for _, task := range tasks {
		evidence, readErr := readTaskEvidence(ctx, transaction, task)
		if readErr != nil {
			return nil, 0, readErr
		}
		observations = append(observations, application.TaskObservation{Task: task, Evidence: evidence})
	}
	stateVersion, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return nil, 0, err
	}
	if err := transaction.Commit(); err != nil {
		return nil, 0, fmt.Errorf("commit task evidence snapshot: %w", err)
	}
	return observations, stateVersion, nil
}

func readTaskEvidence(
	ctx context.Context,
	source queryer,
	task domain.Task,
) (application.TaskEvidenceView, error) {
	evidence := application.TaskEvidenceView{
		Candidate:  application.CandidateEvidenceView{Status: application.CandidateEvidenceNone},
		Activity:   application.ActivityEvidenceView{Status: application.ActivityEvidenceNone},
		Decision:   application.DecisionEvidenceView{Status: application.DecisionEvidenceNone},
		Validation: application.ValidationEvidenceView{Status: application.ValidationEvidenceNotStarted},
		Delivery:   application.DeliveryEvidenceView{Status: application.DeliveryEvidenceNotStarted},
		Cleanup:    application.CleanupEvidenceView{Status: application.CleanupEvidenceNotStarted},
		Authority: application.TaskAuthorityView{
			ManagedRunID: task.ManagedRunID, WorkspaceLeaseID: task.WorkspaceLeaseID,
			ExecutionAttachmentID: task.ExecutionAttachmentID,
		},
	}
	if err := readCandidateDiagnostic(ctx, source, task, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	if err := readReportDiagnostic(ctx, source, task.Handle, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	if err := readDecisionDiagnostic(ctx, source, task.Handle, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	if err := readValidationDiagnostic(ctx, source, task.Handle, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	if err := readDeliveryDiagnostic(ctx, source, task, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	if err := readCleanupDiagnostic(ctx, source, task, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	if err := readPreparationDiagnostic(ctx, source, task.Handle, &evidence); err != nil {
		return application.TaskEvidenceView{}, err
	}
	return evidence, nil
}

func readCandidateDiagnostic(
	ctx context.Context,
	source queryer,
	task domain.Task,
	evidence *application.TaskEvidenceView,
) error {
	const candidateQuery = `SELECT task_handle, evidence_digest, canonical,
		required_local_checks_json, required_forge_checks_json,
		outcome, reason, judged_at, state_version
	FROM candidate_evidence WHERE task_handle = ?
	ORDER BY state_version DESC, evidence_digest LIMIT 1`
	row, err := scanCandidateEvidence(source.QueryRowContext(ctx, candidateQuery, task.Handle))
	if err == nil {
		sealed, parseErr := domain.ParseDeliveryEvidence(row.canonical, row.digest)
		if parseErr != nil {
			return errors.New("read task evidence: stored candidate evidence is invalid")
		}
		bundle := sealed.Bundle()
		if bundle.TaskHandle != task.Handle || bundle.RepositoryIdentity != task.RepositoryID ||
			bundle.BaseRevision != task.BaseRevision {
			return errors.New("read task evidence: candidate identity differs")
		}
		evidence.Candidate = application.CandidateEvidenceView{
			Status: application.CandidateEvidenceJudged, HeadRevision: bundle.HeadRevision,
			EvidenceDigest: sealed.Digest(),
		}
		evidence.Validation.Status = validationDiagnosticStatus(row.judgment.Outcome)
		evidence.Validation.EvidenceDigest = sealed.Digest()
		if bundle.ForgeEvidence != nil {
			evidence.Delivery.PullRequestID = bundle.ForgeEvidence.PullRequestID
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task candidate evidence: %w", err)
	}
	const reconciliationQuery = `SELECT operation_id, head_revision
		FROM task_candidate_reconciliations WHERE task_handle = ?
		ORDER BY completed_state_version DESC, operation_id LIMIT 1`
	var operationID, headRevision string
	if err := source.QueryRowContext(ctx, reconciliationQuery, task.Handle).Scan(&operationID, &headRevision); err == nil {
		evidence.Candidate = application.CandidateEvidenceView{
			Status: application.CandidateEvidenceReconciled, HeadRevision: headRevision,
			ReconciliationOperationID: operationID,
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task reconciliation evidence: %w", err)
	}
	return nil
}

func validationDiagnosticStatus(outcome domain.CandidateOutcome) application.ValidationEvidenceStatus {
	switch outcome {
	case domain.CandidateAccepted:
		return application.ValidationEvidenceAccepted
	case domain.CandidateRejected:
		return application.ValidationEvidenceRejected
	case domain.CandidateUnknown:
		return application.ValidationEvidenceUnknown
	default:
		return application.ValidationEvidenceUnknown
	}
}

func readReportDiagnostic(
	ctx context.Context,
	source queryer,
	taskHandle string,
	evidence *application.TaskEvidenceView,
) error {
	const query = `SELECT local_report_id, kind, accepted_at FROM reports
		WHERE task_handle = ? ORDER BY state_version DESC, local_report_id LIMIT 1`
	var reportID, acceptedAt string
	var kind domain.WorkerReportKind
	if err := source.QueryRowContext(ctx, query, taskHandle).Scan(&reportID, &kind, &acceptedAt); err == nil {
		accepted, parseErr := parseTime(acceptedAt)
		if parseErr != nil {
			return errors.New("read task report evidence: accepted time is invalid")
		}
		evidence.Activity = application.ActivityEvidenceView{
			Status: application.ActivityEvidenceAuthenticatedReport, ReportID: reportID,
			ReportKind: kind, AcceptedAtMs: accepted.UnixMilli(),
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task report evidence: %w", err)
	}
	return nil
}

func readDecisionDiagnostic(
	ctx context.Context,
	source queryer,
	taskHandle string,
	evidence *application.TaskEvidenceView,
) error {
	const openQuery = `SELECT decision.local_report_id FROM reports decision
		WHERE decision.task_handle = ? AND decision.kind = 'decision'
		AND NOT EXISTS (SELECT 1 FROM reports resolution
			WHERE resolution.task_handle = decision.task_handle
			AND resolution.kind = 'resolution'
			AND resolution.external_key = decision.external_key)
		ORDER BY decision.state_version DESC, decision.local_report_id LIMIT 1`
	var decisionID string
	if err := source.QueryRowContext(ctx, openQuery, taskHandle).Scan(&decisionID); err == nil {
		evidence.Decision = application.DecisionEvidenceView{
			Status: application.DecisionEvidenceOpen, DecisionReportID: decisionID,
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task decision evidence: %w", err)
	}
	const resolvedQuery = `SELECT decision.local_report_id, resolution.local_report_id
		FROM reports decision JOIN reports resolution
		ON resolution.task_handle = decision.task_handle
		AND resolution.kind = 'resolution'
		AND resolution.external_key = decision.external_key
		WHERE decision.task_handle = ? AND decision.kind = 'decision'
		ORDER BY resolution.state_version DESC, resolution.local_report_id LIMIT 1`
	var resolutionID string
	if err := source.QueryRowContext(ctx, resolvedQuery, taskHandle).Scan(&decisionID, &resolutionID); err == nil {
		evidence.Decision = application.DecisionEvidenceView{
			Status:           application.DecisionEvidenceResolved,
			DecisionReportID: decisionID, ResolutionReportID: resolutionID,
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task decision resolution evidence: %w", err)
	}
	return nil
}

func readValidationDiagnostic(
	ctx context.Context,
	source queryer,
	taskHandle string,
	evidence *application.TaskEvidenceView,
) error {
	if evidence.Validation.Status != application.ValidationEvidenceNotStarted {
		return nil
	}
	const query = `SELECT operation_id FROM validation_processes
		WHERE task_handle = ? AND state NOT IN ('exited', 'absent')
		ORDER BY observed_at DESC, operation_id LIMIT 1`
	var operationID string
	if err := source.QueryRowContext(ctx, query, taskHandle).Scan(&operationID); err == nil {
		evidence.Validation = application.ValidationEvidenceView{
			Status: application.ValidationEvidenceRunning, ProcessOperationID: operationID,
		}
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task validation evidence: %w", err)
	}
	return nil
}

func readDeliveryDiagnostic(
	ctx context.Context,
	source queryer,
	task domain.Task,
	evidence *application.TaskEvidenceView,
) error {
	const query = `SELECT operation_id, evidence_ref, delivered_at
		FROM comis_evidence_outbox
		WHERE task_handle = ? AND kind IN ('delivery_reference', 'report_artifact')
		ORDER BY state_version DESC, evidence_ref LIMIT 1`
	var operationID, evidenceRef string
	var deliveredAt sql.NullString
	if err := source.QueryRowContext(ctx, query, task.Handle).Scan(&operationID, &evidenceRef, &deliveredAt); err == nil {
		status := application.DeliveryEvidencePending
		if deliveredAt.Valid && taskDeliveryComplete(task.State) {
			status = application.DeliveryEvidenceDelivered
		}
		evidence.Delivery.Status = status
		evidence.Delivery.EvidenceOperationID = operationID
		evidence.Delivery.EvidenceRef = evidenceRef
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task delivery evidence: %w", err)
	}
	if taskDeliveryComplete(task.State) {
		evidence.Delivery.Status = application.DeliveryEvidenceUnknown
	}
	return nil
}

func taskDeliveryComplete(state domain.TaskState) bool {
	switch state {
	case domain.TaskDelivered, domain.TaskCleanupHeld, domain.TaskCleaned:
		return true
	default:
		return false
	}
}

func readCleanupDiagnostic(
	ctx context.Context,
	source queryer,
	task domain.Task,
	evidence *application.TaskEvidenceView,
) error {
	if err := source.QueryRowContext(ctx, `SELECT COUNT(*) FROM task_cleanup_holds
		WHERE task_handle = ? AND closed_at IS NULL`, task.Handle).Scan(&evidence.Cleanup.OpenHoldCount); err != nil {
		return fmt.Errorf("read task cleanup holds: %w", err)
	}
	const query = `SELECT operation_id, stage FROM task_cleanup_operations
		WHERE task_handle = ? ORDER BY state_version DESC, operation_id LIMIT 1`
	var operationID string
	var stage application.TaskCleanupStage
	err := source.QueryRowContext(ctx, query, task.Handle).Scan(&operationID, &stage)
	if err == nil {
		evidence.Cleanup.OperationID = operationID
		evidence.Cleanup.Status = cleanupDiagnosticStatus(stage)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task cleanup evidence: %w", err)
	}
	if evidence.Cleanup.OpenHoldCount > 0 {
		evidence.Cleanup.Status = application.CleanupEvidenceHeld
	} else if task.State == domain.TaskCleanupHeld && evidence.Cleanup.Status == application.CleanupEvidenceNotStarted {
		evidence.Cleanup.Status = application.CleanupEvidenceUnknown
	}
	return nil
}

func cleanupDiagnosticStatus(stage application.TaskCleanupStage) application.CleanupEvidenceStatus {
	switch stage {
	case application.CleanupPrepared:
		return application.CleanupEvidencePrepared
	case application.CleanupHostReleased:
		return application.CleanupEvidenceHostReleased
	case application.CleanupRemovalAuthorized:
		return application.CleanupEvidenceRemovalAuthorized
	case application.CleanupCompleted:
		return application.CleanupEvidenceCompleted
	default:
		return application.CleanupEvidenceUnknown
	}
}

func readPreparationDiagnostic(
	ctx context.Context,
	source queryer,
	taskHandle string,
	evidence *application.TaskEvidenceView,
) error {
	const query = `SELECT operations.id FROM operations
		JOIN task_preparations ON task_preparations.task_handle = operations.result_ref
		WHERE operations.result_ref = ? AND operations.command = 'PrepareTask'
		AND operations.status = 'completed'
		ORDER BY operations.state_version DESC, operations.id LIMIT 1`
	var operationID string
	if err := source.QueryRowContext(ctx, query, taskHandle).Scan(&operationID); err == nil {
		evidence.Authority.PreparationOperationID = operationID
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read task preparation evidence: %w", err)
	}
	return nil
}
