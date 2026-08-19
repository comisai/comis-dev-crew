package application

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// A list-valued field in a read projection is always an array, never absent and
// never null. The projection is the machine surface, so a consumer counting
// entries must not have to handle three encodings of "none" — the fleet listing
// already emits an empty array and these read the same way.
func TestReadProjections_EncodeAnEmptyListAsAnArray(t *testing.T) {
	clock := func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }

	decisions, err := NewQueries(QueryConfig{
		Repository: &queryRepository{stateVersion: 1},
		Decisions:  &decisionInventoryStub{}, Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	decisionList, err := decisions.ListDecisions(context.Background(), "")
	if err != nil {
		t.Fatalf("ListDecisions() error = %v", err)
	}
	assertJSONArray(t, decisionList, "decisions")

	repairs, err := NewQueries(QueryConfig{
		Repository: &queryRepository{stateVersion: 1},
		Repairs:    &repairAuthorityStub{}, ReconciliationWorkspaces: &queryReconciliationInspector{},
		Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	survey, err := repairs.SurveyRepairs(context.Background(), "")
	if err != nil {
		t.Fatalf("SurveyRepairs() error = %v", err)
	}
	assertJSONArray(t, survey, "tasks")

	task := diffTaskFixture()
	diffs, err := NewQueries(QueryConfig{
		Repository: &queryRepository{
			tasks: []domain.Task{task}, stateVersion: 1,
			preparation: ManagedRunPreparation{RequestedWorkspaceRoot: "/approved/worktrees/task-0001"},
		},
		TaskDiffs: &taskDiffStub{}, Clock: clock,
	})
	if err != nil {
		t.Fatalf("NewQueries() error = %v", err)
	}
	view, err := diffs.DiffTask(context.Background(), task.Handle)
	if err != nil {
		t.Fatalf("DiffTask() error = %v", err)
	}
	assertJSONArray(t, view, "committed")
	assertJSONArray(t, view, "uncommitted")
}

func assertJSONArray(t *testing.T, projection any, field string) {
	t.Helper()
	encoded, err := json.Marshal(projection)
	if err != nil {
		t.Fatalf("marshal projection: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	raw, present := decoded[field]
	if !present {
		t.Fatalf("%q is absent from %s", field, encoded)
	}
	if !strings.HasPrefix(string(raw), "[") {
		t.Errorf("%q = %s, want an array", field, raw)
	}
}
