package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartupReconcilerUsesInjectedUTCServiceTime(t *testing.T) {
	now := time.Date(2026, time.August, 9, 16, 40, 0, 0, time.FixedZone("service-local", 3*60*60))
	store := &startupStore{result: StartupReconciliation{TasksMarkedUnknown: 2, OperationsMarkedUnknown: 1, StateVersion: 9}}
	reconciler, err := NewStartupReconciler(StartupReconcilerConfig{Store: store, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("NewStartupReconciler() error = %v", err)
	}
	result, err := reconciler.Reconcile(context.Background())
	if err != nil || result != store.result {
		t.Fatalf("Reconcile() = %#v, %v, want %#v", result, err, store.result)
	}
	if store.at != now.UTC() {
		t.Fatalf("reconcile time = %v, want UTC %v", store.at, now.UTC())
	}
}

func TestStartupReconcilerRejectsMissingDependenciesAndCancellation(t *testing.T) {
	if _, err := NewStartupReconciler(StartupReconcilerConfig{}); err == nil {
		t.Fatal("NewStartupReconciler(empty) error = nil")
	}
	reconciler, err := NewStartupReconciler(StartupReconcilerConfig{Store: &startupStore{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewStartupReconciler() error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reconciler.Reconcile(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile(cancelled) error = %v, want context.Canceled", err)
	}
	//lint:ignore SA1012 The application boundary must reject nil before store access.
	if _, err := reconciler.Reconcile(nil); err == nil {
		t.Fatal("Reconcile(nil) error = nil")
	}
}

type startupStore struct {
	result StartupReconciliation
	at     time.Time
}

func (store *startupStore) ReconcileStartup(_ context.Context, at time.Time) (StartupReconciliation, error) {
	store.at = at
	return store.result, nil
}
