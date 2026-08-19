package localapi

import "context"

// dispatchObservation serves the bounded read surfaces. It reports whether it
// handled the method so the caller can fall through to the transition surface.
func (handler *Handler) dispatchObservation(ctx context.Context, request Request) (Outcome, bool) {
	switch request.Method {
	case MethodReadTaskLogs:
		var payload ReadTaskLogsInput
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		result, err := handler.queries.ReadTaskLogs(
			ctx, payload.TaskHandle, payload.Source, payload.AfterSequence, payload.Limit,
		)
		return queryOutcome(request.OperationID, result.NextCursor, result, err), true
	case MethodReadEvents:
		var payload ReadEventsInput
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		result, err := handler.queries.ReadEvents(ctx, payload.AfterSequence, payload.Limit, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.NextCursor, result, err), true
	case MethodSurveyRepairs:
		var payload SurveyRepairsInput
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		result, err := handler.queries.SurveyRepairs(ctx, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	case MethodDiffTask:
		var payload taskPayload
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		result, err := handler.queries.DiffTask(ctx, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	case MethodListDecisions:
		var payload ListDecisionsInput
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		result, err := handler.queries.ListDecisions(ctx, payload.TaskHandle)
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	case MethodShowDecision:
		var payload ShowDecisionInput
		if err := decodeObject(request.Payload, &payload); err != nil {
			return invalidPayload(request.OperationID, err), true
		}
		result, err := handler.queries.ShowDecision(ctx, payload.TaskHandle, payload.ExternalKey)
		return queryOutcome(request.OperationID, result.StateVersion, result, err), true
	default:
		return Outcome{}, false
	}
}
