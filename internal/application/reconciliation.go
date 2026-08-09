package application

import (
	"context"
	"errors"
	"time"
)

// StartupReconciliation reports the durable state changes completed before the
// service advertises readiness.
type StartupReconciliation struct {
	TasksMarkedUnknown      int
	OperationsMarkedUnknown int
	StateVersion            int64
}

// StartupReconciliationStore owns the atomic startup recovery transaction.
type StartupReconciliationStore interface {
	ReconcileStartup(context.Context, time.Time) (StartupReconciliation, error)
}

// StartupReconcilerConfig supplies deterministic recovery dependencies.
type StartupReconcilerConfig struct {
	Store StartupReconciliationStore
	Clock Clock
}

// StartupReconciler normalizes service time and invokes durable recovery.
type StartupReconciler struct {
	store StartupReconciliationStore
	clock Clock
}

// NewStartupReconciler creates the application startup recovery boundary.
func NewStartupReconciler(config StartupReconcilerConfig) (*StartupReconciler, error) {
	if config.Store == nil || config.Clock == nil {
		return nil, errors.New("create startup reconciler: store and clock are required")
	}
	return &StartupReconciler{store: config.Store, clock: config.Clock}, nil
}

// Reconcile resolves incomplete durable authority before service readiness.
func (reconciler *StartupReconciler) Reconcile(ctx context.Context) (StartupReconciliation, error) {
	if ctx == nil {
		return StartupReconciliation{}, errors.New("startup reconciliation: context is required")
	}
	if err := ctx.Err(); err != nil {
		return StartupReconciliation{}, err
	}
	return reconciler.store.ReconcileStartup(ctx, reconciler.clock().UTC())
}
