package service

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestFixtureSupervisor_ConsumesPinnedReadyTaskExactlyOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(shortTempDir(t), "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, time.August, 9, 12, 0, 0, 0, time.UTC)
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: serviceRepositoryCatalog{},
		TaskIDs: func() (string, error) { return "task-fixture-supervised", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_supervised", nil },
		PreparationTTL: time.Hour, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := mutations.PrepareTask(ctx, application.PrepareTaskCommand{
		OperationID: "prepare-fixture-supervised", ServiceInstanceID: "service-instance-fixture",
		Shape: domain.ShapeShip, RepositoryID: "product-api", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"Emit the deterministic fixture report sequence."},
		Constraints: []string{"Stop at a validation candidate."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutations.ActivateManagedRun(ctx, application.ActivateManagedRunCommand{
		OperationID: "activate-fixture-supervised", ServiceInstanceID: "service-instance-fixture",
		ManagedRunID: "managed-run-fixture", ExternalRunRef: prepared.Task.Handle,
		RegistrationNonce: prepared.Preparation.RegistrationNonce,
	}); err != nil {
		t.Fatal(err)
	}
	supervisor, err := newFixtureSupervisor(fixtureSupervisorConfig{
		Store: store, Mutations: mutations, Clock: func() time.Time { return now },
		Decision: "use the bounded fixture choice", PollInterval: time.Millisecond,
		NewCredential: func() (string, error) { return "fixture-reporter-credential-0123456789abcdef", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(runContext) }()
	deadline := time.After(5 * time.Second)
	for {
		task, getErr := store.GetTask(ctx, prepared.Task.Handle)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if task.State == domain.TaskValidating {
			if task.ReportCursor != 4 {
				t.Fatalf("fixture report cursor = %d, want 4", task.ReportCursor)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatalf("fixture task state = %q, want validating", task.State)
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("FixtureSupervisor.Run() error = %v, want context cancellation", err)
	}
	reports, err := store.ListAcceptedReports(ctx, prepared.Task.Handle)
	if err != nil || len(reports) != 4 {
		t.Fatalf("accepted reports = %d, %v, want exactly four", len(reports), err)
	}
	startID := fixtureStartOperationID(prepared.Task.Handle)
	operation, err := store.GetOperation(ctx, startID)
	if err != nil || operation.Status != domain.OperationCompleted {
		t.Fatalf("fixture start operation %q = %#v, %v", startID, operation, err)
	}
}

