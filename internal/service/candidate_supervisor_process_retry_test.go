package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

func TestCandidateSupervisorRetriesAbsentValidationProcessWithoutStoppingService(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.git.snapshots = []devgit.CandidateSnapshot{
		fixture.snapshot, fixture.snapshot,
		fixture.snapshot, fixture.snapshot,
	}
	runner := &candidateProcessRetryRunner{successfulReceipt: fixture.runner.receipt}
	config := fixture.config()
	config.Runner = runner
	ctx, cancel := context.WithCancel(context.Background())
	fixture.store.onCommit = cancel
	supervisor, err := newCandidateSupervisor(config)
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	if err := supervisor.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context.Canceled after recovered validation", err)
	}
	if runner.calls != 2 || fixture.store.task.State != domain.TaskCandidateComplete {
		t.Fatalf("validation retry calls=%d final state=%q, want 2 and %q",
			runner.calls, fixture.store.task.State, domain.TaskCandidateComplete)
	}
}

type candidateProcessRetryRunner struct {
	successfulReceipt validation.Receipt
	calls             int
}

func (runner *candidateProcessRetryRunner) Run(
	_ context.Context,
	request validation.RunRequest,
) (validation.Receipt, error) {
	runner.calls++
	if runner.calls == 1 {
		return validation.Receipt{}, fmt.Errorf("run validation: fixed program did not start: %w", validation.ErrProcessAbsent)
	}
	receipt := runner.successfulReceipt
	receipt.OperationID = request.OperationID
	return receipt, nil
}
