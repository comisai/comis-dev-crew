package application

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// The read side never reaches a running worker. These refuse rather than
// returning an empty plan, so a query path that started to deliver a keystroke
// would fail loudly instead of silently planning one.
func (*queryHarnessAdapter) SendInput(context.Context, WorkerInputRequest) (WorkerInputPlan, error) {
	return WorkerInputPlan{}, errors.New("query harness adapter does not deliver input")
}

func (*queryHarnessAdapter) RequestPause(context.Context, WorkerControlRequest) (WorkerInputPlan, error) {
	return WorkerInputPlan{}, errors.New("query harness adapter does not deliver control")
}

func (*queryHarnessAdapter) RequestStop(context.Context, WorkerControlRequest) (WorkerInputPlan, error) {
	return WorkerInputPlan{}, errors.New("query harness adapter does not deliver control")
}

func (*queryHarnessAdapter) ValidateProfile(context.Context, string, domain.TaskShape) error {
	return nil
}

func (*queryHarnessAdapter) Diagnose(context.Context) (HarnessDiagnosis, error) {
	return HarnessDiagnosis{}, errors.New("query harness adapter does not diagnose")
}

// Unknown is the honest answer from a stub that observes nothing.
func (*queryHarnessAdapter) ClassifyProcessRole(TaskProcessObservation) ProcessRoleResult {
	return ProcessRoleResult{Role: ProcessRoleUnknown, Reason: ProcessRoleReasonUnattributed}
}
