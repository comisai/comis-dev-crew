package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestIntegrationPolicyReadFailsClosedAtInputAndStorageBoundaries(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	policyID, err := fixture.store.IntegrationPolicy(context.Background(), "initiative-prepare-0001")
	if err != nil || policyID != "integration-default" {
		t.Fatalf("IntegrationPolicy() = %q, %v", policyID, err)
	}
	if _, err := fixture.store.IntegrationPolicy(context.Background(), "bad handle"); err == nil {
		t.Fatal("IntegrationPolicy(invalid) error = nil")
	}
	//lint:ignore SA1012 The store boundary rejects nil before touching SQLite.
	if _, err := fixture.store.IntegrationPolicy(nil, "initiative-prepare-0001"); err == nil {
		t.Fatal("IntegrationPolicy(nil context) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.store.IntegrationPolicy(cancelled, "initiative-prepare-0001"); !errors.Is(err, context.Canceled) {
		t.Fatalf("IntegrationPolicy(cancelled) error = %v", err)
	}
	if _, err := (*Store)(nil).IntegrationPolicy(context.Background(), "initiative-prepare-0001"); err == nil {
		t.Fatal("IntegrationPolicy(nil store) error = nil")
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.IntegrationPolicy(context.Background(), "initiative-prepare-0001"); err == nil {
		t.Fatal("IntegrationPolicy(closed store) error = nil")
	}
}

func TestIntegrationStoreBoundariesRejectInvalidCancelledAndCollidingOperations(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	valid := fixture.reservationRequest("integration-store-boundary", application.IntegrationMerge)
	//lint:ignore SA1012 The store boundary rejects nil before beginning a transaction.
	if _, err := fixture.store.ReserveIntegrationApplication(nil, valid); err == nil {
		t.Fatal("ReserveIntegrationApplication(nil context) error = nil")
	}
	if _, err := (*Store)(nil).ReserveIntegrationApplication(context.Background(), valid); err == nil {
		t.Fatal("ReserveIntegrationApplication(nil store) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.store.ReserveIntegrationApplication(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReserveIntegrationApplication(cancelled) error = %v", err)
	}
	invalid := valid
	invalid.Command.OperationID = "bad operation"
	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), invalid); err == nil {
		t.Fatal("ReserveIntegrationApplication(invalid) error = nil")
	}
	collision := storeOperation(valid.Command.OperationID, 50)
	if err := fixture.store.RecordOperation(context.Background(), collision); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), valid); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReserveIntegrationApplication(operation collision) error = %v", err)
	}
}

func TestIntegrationReservationRejectsUnavailableStateAndEvidenceRows(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*storedIntegrationFixture)
	}{
		{name: "initiative unknown", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE initiatives SET state = 'unknown'`)
		}},
		{name: "candidate not complete", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE tasks SET state = 'failed' WHERE handle = 'task-component-a'`)
		}},
		{name: "owner not writable", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE tasks SET state = 'paused' WHERE handle = 'task-integration'`)
		}},
		{name: "ready owner dependency not accepted", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE tasks SET state = CASE handle
				WHEN 'task-integration' THEN 'ready'
				WHEN 'task-component-a' THEN 'validating'
				ELSE state END`)
		}},
		{name: "task repository authority differs", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE tasks SET base_revision = ? WHERE handle = 'task-component-a'`, strings.Repeat("e", 40))
		}},
		{name: "candidate preparation missing", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`DELETE FROM task_preparations WHERE task_handle = 'task-component-a'`)
		}},
		{name: "candidate evidence missing", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`DELETE FROM candidate_evidence WHERE task_handle = 'task-component-a'`)
		}},
		{name: "candidate evidence corrupt", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE candidate_evidence SET canonical = x'00' WHERE task_handle = 'task-component-a'`)
		}},
		{name: "candidate evidence rejected", mutate: func(fixture *storedIntegrationFixture) {
			_, _ = fixture.store.db.Exec(`UPDATE candidate_evidence SET outcome = 'rejected' WHERE task_handle = 'task-component-a'`)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			test.mutate(&fixture)
			request := fixture.reservationRequest("integration-state-refusal", application.IntegrationMerge)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err == nil {
				t.Fatal("ReserveIntegrationApplication() error = nil")
			}
		})
	}
}

func TestIntegrationCompletionRejectsAlteredReservationResultAndContext(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-completion-boundary", application.IntegrationMerge)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	validResult := application.IntegrationAdapterResult{
		Outcome: application.IntegrationApplied, PreviousHead: request.Command.ExpectedIntegrationHead,
		ResultingHead: strings.Repeat("d", 40),
	}
	invalidResults := []application.IntegrationAdapterResult{
		{},
		{Outcome: application.IntegrationOutcome("unknown"), PreviousHead: request.Command.ExpectedIntegrationHead},
		{Outcome: application.IntegrationApplied, PreviousHead: request.Command.ExpectedIntegrationHead},
		{Outcome: application.IntegrationConflicted, PreviousHead: request.Command.ExpectedIntegrationHead,
			ConflictPaths: []string{"z.txt", "a.txt"}},
		{Outcome: application.IntegrationConflicted, PreviousHead: request.Command.ExpectedIntegrationHead,
			ConflictPaths: []string{"a.txt", "a.txt"}},
		{Outcome: application.IntegrationConflicted, PreviousHead: request.Command.ExpectedIntegrationHead,
			ResultingHead: strings.Repeat("d", 40), ConflictPaths: []string{"a.txt"}},
	}
	for index, adapterResult := range invalidResults {
		if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
			Reservation: reserved, AdapterResult: adapterResult, At: request.At.Add(time.Second),
		}); err == nil {
			t.Fatalf("CompleteIntegrationApplication(invalid %d) error = nil", index)
		}
	}
	altered := reserved
	altered.Candidate.EvidenceDigest = strings.Repeat("f", 64)
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: altered, AdapterResult: validResult, At: request.At.Add(time.Second),
	}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CompleteIntegrationApplication(altered reservation) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := fixture.store.CompleteIntegrationApplication(cancelled, application.IntegrationCompletion{
		Reservation: reserved, AdapterResult: validResult, At: request.At.Add(time.Second),
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("CompleteIntegrationApplication(cancelled) error = %v", err)
	}
	completed, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved, AdapterResult: validResult, At: request.At.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	changed := validResult
	changed.ResultingHead = strings.Repeat("e", 40)
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved, AdapterResult: changed, At: completed.CompletedAt,
	}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CompleteIntegrationApplication(altered replay) error = %v", err)
	}
}

func TestIntegrationAuthorityLookupReturnsAbsentForUnknownTaskAndRepository(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	initiative, err := fixture.store.GetInitiative(context.Background(), "initiative-prepare-0001")
	if err != nil {
		t.Fatal(err)
	}
	if repositoryID, found := initiativeRepositoryForTask(initiative, "task-unknown"); found || repositoryID != "" {
		t.Fatalf("initiativeRepositoryForTask(unknown) = %q, %t", repositoryID, found)
	}
	if revision := initiativeBaseForRepository(initiative, "repository-unknown"); revision != "" {
		t.Fatalf("initiativeBaseForRepository(unknown) = %q", revision)
	}
}

func TestIntegrationPersistenceFaultsRejectDuplicateRowsChangedConflictsAndClosedStore(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-persistence-boundary", application.IntegrationMerge)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	row, found, err := findIntegrationApplication(context.Background(), fixture.store.db, request.Command.OperationID)
	if err != nil || !found {
		t.Fatalf("findIntegrationApplication() = %#v, %t, %v", row, found, err)
	}
	if err := insertIntegrationApplication(context.Background(), fixture.store.db, row); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("insertIntegrationApplication(duplicate) error = %v", err)
	}
	result := application.IntegrationAdapterResult{
		Outcome: application.IntegrationConflicted, PreviousHead: request.Command.ExpectedIntegrationHead,
		ConflictPaths: []string{"a.txt", "b.txt"},
	}
	completedAt := request.At.Add(time.Second)
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved, AdapterResult: result, At: completedAt,
	}); err != nil {
		t.Fatal(err)
	}
	changed := result
	changed.ConflictPaths = []string{"a.txt", "c.txt"}
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved, AdapterResult: changed, At: completedAt,
	}); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CompleteIntegrationApplication(changed conflict) error = %v", err)
	}
	if err := fixture.store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err == nil {
		t.Fatal("ReserveIntegrationApplication(closed store) error = nil")
	}
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved, AdapterResult: result, At: completedAt,
	}); err == nil {
		t.Fatal("CompleteIntegrationApplication(closed store) error = nil")
	}
}

func TestIntegrationStoredRowsRejectCorruptStatusContentAndTimes(t *testing.T) {
	tests := []struct {
		name   string
		update string
	}{
		{name: "status", update: `UPDATE integration_applications SET status = 'unknown'`},
		{name: "conflicts", update: `UPDATE integration_applications SET conflicts_json = '{'`},
		{name: "evidence time", update: `UPDATE integration_applications SET evidence_expires_at = 'invalid'`},
		{name: "reservation time", update: `UPDATE integration_applications SET reserved_at = 'invalid'`},
		{name: "target preparation", update: `UPDATE integration_applications SET target_preparation_operation_id = 'invalid operation'`},
		{name: "completion time", update: `UPDATE integration_applications SET status = 'applied', resulting_head = '` +
			strings.Repeat("d", 40) + `', completed_at = 'invalid', state_version = 2`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			request := fixture.reservationRequest("integration-corrupt-row", application.IntegrationMerge)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.db.Exec(test.update); err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err == nil {
				t.Fatal("ReserveIntegrationApplication(corrupt row) error = nil")
			}
		})
	}
}

func TestIntegrationCompletionRejectsRegressiveTimeAndNilStore(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-completion-time", application.IntegrationMerge)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	completion := application.IntegrationCompletion{
		Reservation: reserved,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationApplied, PreviousHead: request.Command.ExpectedIntegrationHead,
			ResultingHead: strings.Repeat("d", 40),
		},
		At: reserved.ReservedAt.Add(-time.Second),
	}
	if _, err := fixture.store.CompleteIntegrationApplication(context.Background(), completion); err == nil {
		t.Fatal("CompleteIntegrationApplication(regressive time) error = nil")
	}
	completion.At = request.At.Add(time.Second)
	if _, err := (*Store)(nil).CompleteIntegrationApplication(context.Background(), completion); err == nil {
		t.Fatal("CompleteIntegrationApplication(nil store) error = nil")
	}
}
