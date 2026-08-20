package localapi

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// handle records one boundary crossing around the whole request.
//
// The record is taken here rather than inside dispatch because the refusals an
// operator most needs to see — an unusable envelope, an unknown method, a caller
// reaching for an endpoint it does not hold — are refused before dispatch and
// would otherwise be the only calls that leave no trace anywhere.
func (handler *Handler) handle(ctx context.Context, caller CallerClass, data []byte) Outcome {
	started := handler.clock()
	outcome := handler.serve(ctx, caller, data)
	application.RecordBoundary(handler.logger, boundaryRecord(outcome, data, handler.clock().Sub(started)))
	return outcome
}

// boundaryRecord derives the closed record from the answer that was sent. The
// method is read back from the request envelope rather than threaded through
// every refusal path, so a refusal still names what was asked for.
func boundaryRecord(outcome Outcome, data []byte, elapsed time.Duration) application.BoundaryRecord {
	record := application.BoundaryRecord{
		Boundary: application.BoundaryLocalAPI, Operation: unknownRequestMethod,
		OperationID: outcome.OperationID, DurationMs: elapsed.Milliseconds(),
		Outcome: application.BoundaryCompleted,
	}
	var request Request
	if decodeObject(data, &request) == nil && request.Method.valid() {
		record.Operation = string(request.Method)
	}
	if outcome.Error != nil {
		record.Outcome = application.BoundaryFailed
		record.ErrorKind = outcome.Error.Code
		record.Hint = outcome.Error.Hint
	}
	return record
}
