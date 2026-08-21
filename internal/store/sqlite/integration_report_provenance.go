package sqlite

import (
	"context"
	"database/sql"
	"errors"
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
	initiatives, err := listInitiatives(ctx, transaction)
	if err != nil {
		return fmt.Errorf("verify integration report provenance: %w", err)
	}
	var containing *domain.DevelopmentInitiative
	for index := range initiatives {
		if !initiatives[index].ContainsTask(task.Handle) {
			continue
		}
		if containing != nil {
			return errors.New("verify integration report provenance: task belongs to multiple initiatives")
		}
		containing = &initiatives[index]
	}
	if containing == nil || containing.IntegrationOwnerTask != task.Handle {
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
