package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestServiceComponentsWaitForRecoveryBeforeReadiness(t *testing.T) {
	if err := serveServiceComponents(context.Background(), nil, nil, nil, nil); err == nil ||
		!strings.Contains(err.Error(), "stopped unexpectedly") {
		t.Fatalf("serveServiceComponents(empty) = %v", err)
	}
	componentErr := errors.New("component failed")
	componentRelease := make(chan struct{})
	readyCalled := make(chan struct{})
	componentDone := make(chan error, 1)
	go func() {
		componentDone <- serveServiceComponents(context.Background(), nil, []func(context.Context) error{
			func(context.Context) error {
				<-componentRelease
				return componentErr
			},
			func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}, nil, func() { close(readyCalled) })
	}()
	<-readyCalled
	close(componentRelease)
	if err := <-componentDone; !errors.Is(err, componentErr) {
		t.Fatalf("serveServiceComponents() = %v", err)
	}

	componentStarted := make(chan struct{})
	recoveryGate := make(chan struct{})
	advertised := make(chan struct{})
	runContext, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- serveServiceComponents(runContext, nil, []func(context.Context) error{
			func(ctx context.Context) error {
				close(componentStarted)
				<-ctx.Done()
				return ctx.Err()
			},
		}, func(ctx context.Context) error {
			select {
			case <-recoveryGate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}, func() { close(advertised) })
	}()
	<-componentStarted
	select {
	case <-advertised:
		t.Fatal("service advertised readiness before runtime recovery")
	default:
	}
	close(recoveryGate)
	select {
	case <-advertised:
	case <-time.After(time.Second):
		t.Fatal("service did not advertise readiness after runtime recovery")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("serveServiceComponents(recovery barrier) error = %v", err)
	}
}

func TestServiceComponentsRefuseReadinessFailuresAndEarlyStops(t *testing.T) {
	recoveryErr := errors.New("runtime recovery failed")
	readyCalls := 0
	err := serveServiceComponents(context.Background(), nil, []func(context.Context) error{
		func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}, func(context.Context) error { return recoveryErr }, func() { readyCalls++ })
	if !errors.Is(err, recoveryErr) || readyCalls != 0 {
		t.Fatalf("serveServiceComponents(recovery failure) = %v, ready=%d", err, readyCalls)
	}

	componentRelease := make(chan struct{})
	readinessStarted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- serveServiceComponents(context.Background(), nil, []func(context.Context) error{
			func(context.Context) error {
				<-componentRelease
				return nil
			},
		}, func(ctx context.Context) error {
			close(readinessStarted)
			<-ctx.Done()
			return ctx.Err()
		}, func() { readyCalls++ })
	}()
	<-readinessStarted
	close(componentRelease)
	err = <-done
	if err == nil || !strings.Contains(err.Error(), "stopped unexpectedly") || readyCalls != 0 {
		t.Fatalf("serveServiceComponents(early stop) = %v, ready=%d", err, readyCalls)
	}
}

func TestRuntimeAttachmentRecoveryWaitPreservesFailureAndCancellation(t *testing.T) {
	if err := (*runtimeAttachmentCoordinator)(nil).waitForRecovery(context.Background()); err == nil {
		t.Fatal("nil runtime attachment coordinator waited for recovery")
	}
	coordinator := &runtimeAttachmentCoordinator{recoveryReady: make(chan struct{})}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := coordinator.waitForRecovery(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("waitForRecovery(canceled) error = %v", err)
	}
	recoveryErr := errors.New("recovery unavailable")
	coordinator.recoveryErr = recoveryErr
	close(coordinator.recoveryReady)
	if err := coordinator.waitForRecovery(context.Background()); !errors.Is(err, recoveryErr) {
		t.Fatalf("waitForRecovery(failure) error = %v", err)
	}
}
