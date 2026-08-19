package localapi

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func repairQueriesFixture() *apiQueries {
	return &apiQueries{repairs: application.RepairSurvey{
		SchemaVersion: 1, StateVersion: 9, CapturedAt: time.Unix(1_800_000_000, 0).UTC(),
		Tasks: []application.TaskRepair{{
			TaskHandle: "task-0001", State: domain.TaskUnknown,
			Posture: application.RepairReconcilable,
		}},
	}}
}

// The operator console reads which tasks need reconciling over the owner-only
// endpoint.
func TestClient_ReadsTheReconciliationSurvey(t *testing.T) {
	client := newDecisionClient(t, CallerOperatorCLI, repairQueriesFixture())

	survey, err := client.SurveyRepairs(context.Background(), "read-repairs", SurveyRepairsInput{})
	if err != nil {
		t.Fatalf("SurveyRepairs() error = %v", err)
	}
	if len(survey.Tasks) != 1 || survey.Tasks[0].Posture != application.RepairReconcilable {
		t.Fatalf("survey = %#v", survey)
	}
}

// The survey names which tasks are stuck and where their worktrees stand, so it
// is operator detail rather than a model-facing view.
func TestHandler_RefusesTheReconciliationSurveyFromTheModelFacade(t *testing.T) {
	client := newDecisionClient(t, CallerMCPFacade, repairQueriesFixture())
	if _, err := client.SurveyRepairs(context.Background(), "read-repairs", SurveyRepairsInput{}); err == nil {
		t.Error("SurveyRepairs(MCP facade) error = nil, want a refusal")
	}
}

func TestSurveyRepairsMethod_IsAValidOperatorOnlyRead(t *testing.T) {
	if !MethodSurveyRepairs.valid() || MethodSurveyRepairs.SideEffect() != SideEffectRead {
		t.Fatalf("SurveyRepairs method = %q, side effect = %q", MethodSurveyRepairs, MethodSurveyRepairs.SideEffect())
	}
	if methodAllowed(CallerMCPFacade, MethodSurveyRepairs) {
		t.Error("SurveyRepairs is reachable from the model facade")
	}
	if !methodAllowed(CallerOperatorCLI, MethodSurveyRepairs) {
		t.Error("SurveyRepairs is unreachable from the operator console")
	}
}
