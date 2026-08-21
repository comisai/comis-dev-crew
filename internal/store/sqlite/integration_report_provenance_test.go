package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestIntegrationOwnerCompletionRequiresAppliedPredecessorReceipts(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	integration, err := fixture.store.GetTask(context.Background(), "task-integration")
	if err != nil {
		t.Fatal(err)
	}
	report := sqliteWorkerReport(integration, "report-integration-without-receipts", domain.ReportCandidateComplete)
	mutation := directReportMutation(integration, report, fixture.at.AddDate(0, 0, 1))

	if _, err := fixture.store.CommitReport(context.Background(), mutation); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("CommitReport(integration without receipts) error = %v, want ErrPrecondition", err)
	}
	unchanged, err := fixture.store.GetTask(context.Background(), integration.Handle)
	if err != nil || unchanged.State != domain.TaskWorking || unchanged.ReportCursor != integration.ReportCursor {
		t.Fatalf("integration task after refusal = %#v, %v", unchanged, err)
	}
	reports, err := fixture.store.ListAcceptedReports(context.Background(), integration.Handle)
	if err != nil || len(reports) != 0 {
		t.Fatalf("integration reports after refusal = %#v, %v", reports, err)
	}
}
