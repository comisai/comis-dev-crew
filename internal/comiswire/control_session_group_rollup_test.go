package comiswire

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestControlConnectionReadsGroupRollupOnTheAuthenticatedSession(t *testing.T) {
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
		OperationID:      "operation_group_rollup_ok",
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
