package comiswire

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// DecisionRaiserConfig binds re-surfacing to the authenticated control lane.
type DecisionRaiserConfig struct {
	Sender ReportSender
}

// DecisionRaiser puts one open decision back in front of the liaison over the
// generic attention path the question took the first time.
//
// It owns no durable state. The surfacing ledger is written only after the host
// acknowledges, so a lost process repeats a question rather than losing one, and
// the identity below is what lets the host recognize the repeat.
type DecisionRaiser struct {
	sender ReportSender
}

// NewDecisionRaiser validates the control dependency before supervision.
func NewDecisionRaiser(config DecisionRaiserConfig) (*DecisionRaiser, error) {
	if config.Sender == nil {
		return nil, errors.New("create decision raiser: sender is required")
	}
	return &DecisionRaiser{sender: config.Sender}, nil
}

// RaiseOpenDecision sends one attention report and returns only on exact host
// acknowledgement. Any other outcome leaves the surfacing unrecorded, so the
// next sweep asks again under the same identity.
func (raiser *DecisionRaiser) RaiseOpenDecision(ctx context.Context, decision application.OpenDecision) error {
	if ctx == nil {
		return errors.New("raise open decision: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := decisionRaiseRequest(decision)
	if err != nil {
		return err
	}
	result, err := raiser.sender.Report(ctx, request)
	if err != nil {
		return fmt.Errorf("raise open decision: %w", err)
	}
	if result.ManagedRunID != request.ManagedRunID || result.ServiceReportID != request.ServiceReportID {
		return errors.New("raise open decision: acknowledgement identity differs")
	}
	return nil
}

// decisionRaiseRequest builds the attention report for one surfacing.
//
// Every identity is derived from the decision plus the number of surfacings
// already recorded, so a retry after an uncertain send reproduces the exact
// request while the surfacing that follows a recorded one is a new report.
func decisionRaiseRequest(decision application.OpenDecision) (ReportRequestParams, error) {
	if domain.ValidateTaskHandle(decision.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("managedRunId", decision.ManagedRunID) != nil ||
		domain.ValidateDecisionKey(decision.ExternalKey) != nil {
		return ReportRequestParams{}, errors.New("raise open decision: decision identity is invalid")
	}
	if decision.SurfaceCount < 0 {
		return ReportRequestParams{}, errors.New("raise open decision: surfacing count is invalid")
	}
	if decision.Summary == "" {
		return ReportRequestParams{}, errors.New("raise open decision: question is unavailable")
	}
	if len([]byte(decision.Summary))+len([]byte(decision.Details)) > MaxReportBytes {
		return ReportRequestParams{}, errors.New("raise open decision: question exceeds the host byte limit")
	}
	operationID, serviceReportID := decisionRaiseIDs(decision)
	key := decision.ExternalKey
	request := ReportRequestParams{
		OperationID: OperationID(operationID), ManagedRunID: ManagedRunID(decision.ManagedRunID),
		ServiceReportID: ServiceReportID(serviceReportID), Kind: ReportKindAttention,
		ExternalKey: &key, Summary: decision.Summary,
	}
	if decision.Details != "" {
		details := decision.Details
		request.Details = &details
	}
	document := ReportRequest{
		JSONRPC: JSONRPCVersion, ID: request.OperationID,
		Method: MethodManagedRunsReport, Params: request,
	}
	if err := validateGeneratedDocument(schemaReportRequest, document); err != nil {
		return ReportRequestParams{}, fmt.Errorf("raise open decision: invalid attention report: %w", err)
	}
	return request, nil
}

func decisionRaiseIDs(decision application.OpenDecision) (string, string) {
	digest := sha256.Sum256([]byte(
		decision.TaskHandle + "\x00" + decision.ExternalKey + "\x00" + strconv.Itoa(decision.SurfaceCount),
	))
	identity := fmt.Sprintf("%x", digest[:16])
	return "attention-" + identity, "service-attention-" + identity
}
