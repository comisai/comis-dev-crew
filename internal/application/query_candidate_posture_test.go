package application

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// TestQueries_ExplainNamesEveryCandidateJudgment covers the closed judgment
// vocabulary rather than the three verdicts somebody happened to wire.
//
// A candidate that stalls is the case an operator most needs explained, and the
// generic lifecycle text it otherwise falls back to does not merely omit the
// reason — it states that no durable blocking reason is recorded, while the
// reason sits in durable candidate evidence. An operator who believes that
// sentence stops looking; one who does not has to read the database by hand.
func TestQueries_ExplainNamesEveryCandidateJudgment(t *testing.T) {
	cases := []struct {
		reason  domain.CandidateReason
		outcome domain.CandidateOutcome
		state   domain.TaskState
		actions []NextAction
	}{
		{domain.CandidateEvidenceInvalid, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateEvidenceStale, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateEvidenceConflicting, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateDecisionUnresolved, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask, ActionResolveBlock}},
		{domain.CandidateValidationMissing, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateValidationUnknown, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateForgeMissing, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateForgeUnknown, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateReportMissing, domain.CandidateUnknown, domain.TaskValidating, []NextAction{ActionInspectTask}},
		{domain.CandidateValidationFailed, domain.CandidateRejected, domain.TaskFailed, []NextAction{ActionInspectTask}},
		{domain.CandidateForgeFailed, domain.CandidateRejected, domain.TaskFailed, []NextAction{ActionInspectTask}},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.reason), func(t *testing.T) {
			task := queryTask("task-candidate-posture", testCase.state, 11)
			repository := &queryRepository{
				tasks:             []domain.Task{task},
				candidateEvidence: queryCandidateEvidence(t, task, time.Now().UTC()),
				candidateJudgment: domain.CandidateJudgment{Outcome: testCase.outcome, Reason: testCase.reason},
			}
			queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
			if err != nil {
				t.Fatalf("NewQueries() error = %v", err)
			}
			explanation, err := queries.ExplainTask(context.Background(), task.Handle)
			if err != nil {
				t.Fatalf("ExplainTask() error = %v", err)
			}
			want := "candidate_" + string(testCase.reason)
			if explanation.ReasonCode != want {
				t.Errorf("ReasonCode = %q, want %q", explanation.ReasonCode, want)
			}
			if strings.Contains(explanation.LikelyRootCause, "No durable blocking reason is recorded") {
				t.Errorf("LikelyRootCause claims nothing is recorded while judgment %q is durable", testCase.reason)
			}
			if strings.TrimSpace(explanation.LikelyRootCause) == "" || strings.TrimSpace(explanation.Explanation) == "" {
				t.Errorf("explanation is empty: %#v", explanation)
			}
			if !sameActions(explanation.NextSafeActions, testCase.actions) {
				t.Errorf("NextSafeActions = %v, want %v", explanation.NextSafeActions, testCase.actions)
			}
		})
	}
}

// TestQueries_ExplainKeepsCandidateRootCausesContentFree guards the surface
// rather than the wording. Explain is reachable from the model facade, so a
// root cause that quoted a check name, branch or path would put task content in
// front of a worker through a read that exists for the operator.
func TestQueries_ExplainKeepsCandidateRootCausesContentFree(t *testing.T) {
	forbidden := []string{"ci/unit", "github-pr-17", "product-api", "go-default"}
	reasons := []domain.CandidateReason{
		domain.CandidateEvidenceInvalid, domain.CandidateEvidenceStale, domain.CandidateEvidenceConflicting,
		domain.CandidateDecisionUnresolved, domain.CandidateValidationMissing, domain.CandidateValidationUnknown,
		domain.CandidateForgeMissing, domain.CandidateForgeUnknown, domain.CandidateReportMissing,
		domain.CandidateValidationFailed, domain.CandidateForgeFailed,
	}
	for _, reason := range reasons {
		task := queryTask("task-candidate-content", domain.TaskValidating, 12)
		outcome := domain.CandidateUnknown
		if reason == domain.CandidateValidationFailed || reason == domain.CandidateForgeFailed {
			outcome = domain.CandidateRejected
			task = queryTask("task-candidate-content", domain.TaskFailed, 12)
		}
		repository := &queryRepository{
			tasks:             []domain.Task{task},
			candidateEvidence: queryCandidateEvidence(t, task, time.Now().UTC()),
			candidateJudgment: domain.CandidateJudgment{Outcome: outcome, Reason: reason},
		}
		queries, err := NewQueries(QueryConfig{Repository: repository, Clock: time.Now})
		if err != nil {
			t.Fatalf("NewQueries() error = %v", err)
		}
		explanation, err := queries.ExplainTask(context.Background(), task.Handle)
		if err != nil {
			t.Fatalf("ExplainTask() error = %v", err)
		}
		text := explanation.Explanation + " " + explanation.LikelyRootCause
		for _, value := range forbidden {
			if strings.Contains(text, value) {
				t.Errorf("reason %q leaked %q into an explanation reachable from the model facade", reason, value)
			}
		}
	}
}

func sameActions(actual, want []NextAction) bool {
	if len(actual) != len(want) {
		return false
	}
	for index := range want {
		if actual[index] != want[index] {
			return false
		}
	}
	return true
}
