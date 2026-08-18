package application

import (
	"context"
	"errors"
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
