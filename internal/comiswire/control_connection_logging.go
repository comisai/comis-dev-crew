package comiswire

import (
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func controlConnectionClock(config ControlConnectionConfig) func() time.Time {
	if config.Clock != nil {
		return config.Clock
	}
	return time.Now
}

func (connection *ControlConnection) recordConnectionCompletion(started time.Time) {
	clock := controlConnectionClock(connection.config)
	application.RecordBoundary(connection.config.Logger, application.BoundaryRecord{
		Boundary: application.BoundaryControl, Operation: string(MethodCapabilityServicesHandshake),
		OperationID: string(connection.config.HandshakeOperationID), DurationMs: clock().Sub(started).Milliseconds(),
		Outcome: application.BoundaryCompleted,
	})
}

func (connection *ControlConnection) recordConnectionFailure(connectErr error, started time.Time) {
	clock := controlConnectionClock(connection.config)
	errorKind := domain.ErrorUnavailable
	failureCause := application.BoundaryFailureControlConnectionUnavailable
	hint := "inspect the Comis control endpoint and retry after it is healthy"
	var remote RPCError
	if errors.As(connectErr, &remote) && remote.Kind == ErrorKindPreconditionFailed {
		errorKind = domain.ErrorPrecondition
		failureCause = application.BoundaryFailureControlHandshakePrecondition
		hint = "verify the configured control scope set and pinned protocol identity"
	}
	application.RecordBoundary(connection.config.Logger, application.BoundaryRecord{
		Boundary: application.BoundaryControl, Operation: string(MethodCapabilityServicesHandshake),
		OperationID: string(connection.config.HandshakeOperationID), DurationMs: clock().Sub(started).Milliseconds(),
		Outcome: application.BoundaryFailed, ErrorKind: errorKind, Hint: hint, FailureCause: failureCause,
	})
}
