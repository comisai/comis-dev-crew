package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func requireIntegrationReportProvenance(
	ctx context.Context,
	transaction *sql.Tx,
	task domain.Task,
	reportKind domain.WorkerReportKind,
) error {
	if reportKind != domain.ReportCandidateComplete {
		return nil
	}
	containing, found, err := initiativeForTask(ctx, transaction, task.Handle)
	if err != nil {
		return fmt.Errorf("verify integration report provenance: %w", err)
	}
	if !found || containing.IntegrationOwnerTask != task.Handle {
		return nil
	}
	for _, edge := range containing.Edges {
		if edge.Kind != domain.EdgeIntegratesAfter || edge.ToTaskHandle != task.Handle {
			continue
		}
		var completed int
		if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*)
			FROM integration_applications AS application
			JOIN candidate_evidence AS evidence
			  ON evidence.task_handle = application.candidate_task_handle
			 AND evidence.evidence_digest = application.evidence_digest
			WHERE application.initiative_handle = ? AND application.integration_task_handle = ?
			AND application.candidate_task_handle = ? AND application.status IN ('applied', 'conflicted')
			AND evidence.outcome = 'accepted'
			AND evidence.state_version = (
			  SELECT MAX(latest.state_version) FROM candidate_evidence AS latest
			  WHERE latest.task_handle = application.candidate_task_handle
			)`,
			containing.Handle, task.Handle, edge.FromTaskHandle,
		).Scan(&completed); err != nil {
			return fmt.Errorf("verify integration report provenance: %w", err)
		}
		if completed == 0 {
			return fmt.Errorf("integration predecessor %q has no completed application receipt: %w",
				edge.FromTaskHandle, application.ErrPrecondition)
		}
	}
	return nil
}
