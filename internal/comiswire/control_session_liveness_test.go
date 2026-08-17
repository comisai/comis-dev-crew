package comiswire

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"
)

// The heartbeat exchange lives beside the other control-session tests but in its
// own file: liveness is the one call whose whole purpose is to be sent
// repeatedly and never retried, and its assertions read better away from the
// report and evidence exchanges they must not be confused with.

func TestControlConnectionHeartbeatRejectsInvalidUncertainAndMismatchedOutcomes(t *testing.T) {
	connection := &ControlConnection{
		config:  ControlConnectionConfig{Credential: controlTestBearer, RequestTimeout: 50 * time.Millisecond},
		changed: make(chan struct{}),
	}
	valid := HeartbeatRequestParams{
		OperationID: "operation_heartbeat_scope", ManagedRunID: "managed-run_scope",
		ObservedAtMs: 1_800_000_000_000,
	}
	if _, err := connection.Heartbeat(nilControlContext(), valid); err == nil {
		t.Fatal("Heartbeat(nil) error = nil")
	}
	invalid := valid
	invalid.ManagedRunID = ""
	if _, err := connection.Heartbeat(context.Background(), invalid); err == nil {
		t.Fatal("Heartbeat(invalid) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := connection.Heartbeat(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("Heartbeat(cancelled) error = %v", err)
	}

	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	connection.config.RequestTimeout = time.Second
	connection.publish(session)
	serveContext, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.serve(serveContext) }()
	t.Cleanup(func() {
		stop()
		_ = host.Close()
		<-done
	})
	hostDone := make(chan error, 1)
	go func() {
		var request authenticatedHeartbeatRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		// A beat that comes back naming a different run is not this run's proof
		// of life, and accepting it would let one run's liveness stand in for
		// another's.
		hostDone <- writeControlFrame(host, HeartbeatResponse{
			JSONRPC: JSONRPCVersion, ID: request.ID,
			Result: HeartbeatResponseResult{
				ManagedRunID: "managed-run_forged", AcceptedAtMs: 1_800_000_000_001,
				LastHeartbeatAtMs: request.Params.ObservedAtMs,
			},
		})
	}()
	if _, err := connection.Heartbeat(context.Background(), valid); err == nil {
		t.Fatal("Heartbeat(mismatched acknowledgement) error = nil")
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exchange: %v", err)
	}
}

func TestControlConnectionHeartbeatReturnsTheHostAcknowledgement(t *testing.T) {
	connection := &ControlConnection{
		config:  ControlConnectionConfig{Credential: controlTestBearer, RequestTimeout: time.Second},
		changed: make(chan struct{}),
	}
	service, host := net.Pipe()
	session := newControlSession(service, controlTestBearer, controlHandlerStub{}, time.Second)
	connection.publish(session)
	serveContext, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- session.serve(serveContext) }()
	t.Cleanup(func() {
		stop()
		_ = host.Close()
		<-done
	})
	hostDone := make(chan error, 1)
	go func() {
		var request authenticatedHeartbeatRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		if request.Bearer != controlTestBearer {
			hostDone <- errors.New("heartbeat arrived without the instance credential")
			return
		}
		hostDone <- writeControlFrame(host, HeartbeatResponse{
			JSONRPC: JSONRPCVersion, ID: request.ID,
			Result: HeartbeatResponseResult{
				ManagedRunID: request.Params.ManagedRunID, AcceptedAtMs: 1_800_000_000_005,
				LastHeartbeatAtMs: request.Params.ObservedAtMs,
			},
		})
	}()

	result, err := connection.Heartbeat(context.Background(), HeartbeatRequestParams{
		OperationID: "operation_heartbeat_ok", ManagedRunID: "managed-run_ok",
		ObservedAtMs: 1_800_000_000_000,
	})

	if err != nil {
		t.Fatalf("Heartbeat error = %v", err)
	}
	if result.ManagedRunID != "managed-run_ok" || result.LastHeartbeatAtMs != 1_800_000_000_000 {
		t.Errorf("Heartbeat result = %+v", result)
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exchange: %v", err)
	}
}

func TestControlSessionDispatchesCancelAndFailsClosed(t *testing.T) {
	valid := CancelRequest{
		JSONRPC: JSONRPCVersion, ID: "operation_cancel_a", Method: MethodManagedRunsCancel,
		Params: CancelRequestParams{
			OperationID: "operation_cancel_a", ManagedRunID: "managed-run_a",
			Reason: "owner_cancelled",
		},
	}
	t.Run("success", func(t *testing.T) {
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedCancelRequest{
			CancelRequest: valid, Bearer: controlTestBearer,
		})
		var cancelled CancelResponse
		if err := json.Unmarshal(response, &cancelled); err != nil {
			t.Fatal(err)
		}
		if cancelled.Result.ManagedRunID != valid.Params.ManagedRunID ||
			cancelled.Result.State != CancelStateCancelled {
			t.Fatalf("cancel response = %#v", cancelled)
		}
	})
	t.Run("altered envelope operation", func(t *testing.T) {
		// The envelope id and the params operation id are one identity; letting
		// them differ would let a caller address a run it did not name.
		altered := valid
		altered.ID = "operation_envelope_other"
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedCancelRequest{
			CancelRequest: altered, Bearer: controlTestBearer,
		})
		if !bytes.Contains(response, []byte("invalid_request")) {
			t.Fatalf("altered cancel envelope response = %s", response)
		}
	})
	t.Run("wrong credential", func(t *testing.T) {
		response := dispatchControlTestFrame(t, controlHandlerStub{}, authenticatedCancelRequest{
			CancelRequest: valid, Bearer: "not-the-instance-credential",
		})
		if !bytes.Contains(response, []byte("unauthorized_instance")) {
			t.Fatalf("unauthenticated cancel response = %s", response)
		}
	})
}
