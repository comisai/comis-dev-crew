package comiswire

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func newPublishedGroupRollupConnection(t *testing.T) (*ControlConnection, net.Conn) {
	t.Helper()
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
	return connection, host
}

func TestControlConnectionReadsGroupRollupOnTheAuthenticatedSession(t *testing.T) {
	connection, host := newPublishedGroupRollupConnection(t)
	hostDone := make(chan error, 1)
	go func() {
		active, succeeded := int64(2), int64(1)
		var request authenticatedGroupGetHostRollupRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		if request.Bearer != controlTestBearer {
			hostDone <- errors.New("group rollup arrived without the instance credential")
			return
		}
		hostDone <- writeControlFrame(host, GroupGetHostRollupResponse{
			JSONRPC: JSONRPCVersion,
			ID:      request.ID,
			Result: GroupGetHostRollupResponseResult{
				ManagedRunGroupID: request.Params.ManagedRunGroupID,
				MemberManagedRunIds: []string{
					"managed-run_backend", "managed-run_frontend", "managed-run_integration",
				},
				StateCounts: GroupGetHostRollupResponseResultStateCounts{Active: &active, Succeeded: &succeeded},
				UpdatedAtMs: 1_800_000_000_005,
			},
		})
	}()

	result, err := connection.GroupHostRollup(context.Background(), GroupGetHostRollupRequestParams{
		OperationID:       "operation_group_rollup_ok",
		ManagedRunGroupID: "managed-run-group_ok",
	})

	if err != nil {
		t.Fatalf("GroupHostRollup() error = %v", err)
	}
	if result.ManagedRunGroupID != "managed-run-group_ok" || len(result.MemberManagedRunIds) != 3 ||
		result.StateCounts.Active == nil || *result.StateCounts.Active != 2 ||
		result.StateCounts.Succeeded == nil || *result.StateCounts.Succeeded != 1 {
		t.Fatalf("GroupHostRollup() = %#v", result)
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exchange: %v", err)
	}
}

func TestControlConnectionRejectsInvalidGroupRollupInputsBeforeTransport(t *testing.T) {
	connection := &ControlConnection{changed: make(chan struct{})}

	if _, err := connection.GroupHostRollup(nil, GroupGetHostRollupRequestParams{}); err == nil ||
		!strings.Contains(err.Error(), "context is required") {
		t.Fatalf("GroupHostRollup(nil) error = %v", err)
	}
	if _, err := connection.GroupHostRollup(context.Background(), GroupGetHostRollupRequestParams{}); err == nil ||
		!strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("GroupHostRollup(invalid request) error = %v", err)
	}
}

func TestControlConnectionRejectsMismatchedGroupRollupIdentity(t *testing.T) {
	connection, host := newPublishedGroupRollupConnection(t)
	hostDone := make(chan error, 1)
	go func() {
		var request authenticatedGroupGetHostRollupRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		hostDone <- writeControlFrame(host, GroupGetHostRollupResponse{
			JSONRPC: JSONRPCVersion,
			ID:      request.ID,
			Result: GroupGetHostRollupResponseResult{
				ManagedRunGroupID:   "managed-run-group_other",
				MemberManagedRunIds: []string{"managed-run_backend"},
				UpdatedAtMs:         1_800_000_000_005,
			},
		})
	}()

	_, err := connection.GroupHostRollup(context.Background(), GroupGetHostRollupRequestParams{
		OperationID: "operation_group_rollup_mismatch", ManagedRunGroupID: "managed-run-group_expected",
	})

	if err == nil || !strings.Contains(err.Error(), "acknowledgement identity differs") {
		t.Fatalf("GroupHostRollup(mismatched identity) error = %v", err)
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exchange: %v", err)
	}
}

func TestControlConnectionRejectsSchemaInvalidGroupRollupResponse(t *testing.T) {
	connection, host := newPublishedGroupRollupConnection(t)
	hostDone := make(chan error, 1)
	go func() {
		var request authenticatedGroupGetHostRollupRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		hostDone <- writeControlFrame(host, GroupGetHostRollupResponse{
			JSONRPC: JSONRPCVersion,
			ID:      request.ID,
			Result: GroupGetHostRollupResponseResult{
				ManagedRunGroupID:   request.Params.ManagedRunGroupID,
				MemberManagedRunIds: []string{},
				UpdatedAtMs:         1_800_000_000_005,
			},
		})
	}()

	_, err := connection.GroupHostRollup(context.Background(), GroupGetHostRollupRequestParams{
		OperationID: "operation_group_rollup_invalid", ManagedRunGroupID: "managed-run-group_expected",
	})

	if err == nil || !strings.Contains(err.Error(), "invalid response") {
		t.Fatalf("GroupHostRollup(invalid response) error = %v", err)
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exchange: %v", err)
	}
}

func TestControlConnectionMapsGroupRollupOntoTheApplicationPort(t *testing.T) {
	connection, host := newPublishedGroupRollupConnection(t)
	hostDone := make(chan error, 1)
	go func() {
		active, waiting := int64(1), int64(2)
		var request authenticatedGroupGetHostRollupRequest
		if err := readControlFrame(host, &request); err != nil {
			hostDone <- err
			return
		}
		hostDone <- writeControlFrame(host, GroupGetHostRollupResponse{
			JSONRPC: JSONRPCVersion,
			ID:      request.ID,
			Result: GroupGetHostRollupResponseResult{
				ManagedRunGroupID: request.Params.ManagedRunGroupID,
				MemberManagedRunIds: []string{
					"managed-run_backend", "managed-run_frontend", "managed-run_integration",
				},
				StateCounts: GroupGetHostRollupResponseResultStateCounts{
					Active: &active, Waiting: &waiting,
				},
				AttentionCount: 2, ActiveCustodyCount: 1, UpdatedAtMs: 1_800_000_000_005,
			},
		})
	}()

	result, err := connection.ReadInitiativeHostRollup(context.Background(), application.InitiativeHostRollupRequest{
		OperationID: "operation_group_rollup_port", ManagedRunGroupID: "managed-run-group_expected",
	})

	if err != nil || result.ManagedRunGroupID != "managed-run-group_expected" ||
		result.StateCounts.Active != 1 || result.StateCounts.Waiting != 2 ||
		result.AttentionCount != 2 || result.ActiveCustodyCount != 1 || result.UpdatedAtMs != 1_800_000_000_005 {
		t.Fatalf("ReadInitiativeHostRollup() = %#v, %v", result, err)
	}
	if err := <-hostDone; err != nil {
		t.Fatalf("host exchange: %v", err)
	}
}
