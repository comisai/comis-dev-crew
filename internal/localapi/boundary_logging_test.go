package localapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type recordingLogger struct {
	records []application.BoundaryRecord
}

func (logger *recordingLogger) Record(record application.BoundaryRecord) {
	logger.records = append(logger.records, record)
}

func loggingHandler(t *testing.T, logger application.BoundaryLogger) *Handler {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{
		Queries: decisionQueriesFixture(), Clock: time.Now, Logger: logger,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler
}

func request(t *testing.T, method Method, operationID string) []byte {
	t.Helper()
	encoded, err := json.Marshal(Request{
		ProtocolVersion: ProtocolVersion, OperationID: operationID, Method: method,
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	return encoded
}

// TestBoundaryLogging_RecordsOneCompletionPerCall gives an operator the line
// that says a call arrived, how long it took, and which one it was.
//
// Every console command and every model tool call crosses this handler, so
// without a record here the service can serve for weeks and leave nothing an
// operator could reconstruct a slow or failing call from.
func TestBoundaryLogging_RecordsOneCompletionPerCall(t *testing.T) {
	logger := &recordingLogger{}
	handler := loggingHandler(t, logger)

	outcome := handler.handle(context.Background(), CallerOperatorCLI, request(t, MethodListDecisions, "operation-log-0001"))
	if outcome.Status != domain.OperationCompleted {
		t.Fatalf("handle() status = %q, want completed", outcome.Status)
	}
	if len(logger.records) != 1 {
		t.Fatalf("recorded %d boundary records, want exactly 1", len(logger.records))
	}
	record := logger.records[0]
	if record.Boundary != application.BoundaryLocalAPI {
		t.Errorf("boundary = %q, want the local API", record.Boundary)
	}
	if record.Operation != string(MethodListDecisions) {
		t.Errorf("operation = %q, want %q", record.Operation, MethodListDecisions)
	}
	if record.OperationID != "operation-log-0001" {
		t.Errorf("operation ID = %q", record.OperationID)
	}
	if record.Outcome != application.BoundaryCompleted {
		t.Errorf("outcome = %q, want completed", record.Outcome)
	}
	if record.DurationMs < 0 {
		t.Errorf("duration = %d, want a non-negative measurement", record.DurationMs)
	}
	if record.ErrorKind != "" || record.Hint != "" {
		t.Errorf("a completion carried failure fields: %#v", record)
	}
}

// TestBoundaryLogging_RecordsEveryRefusalWithItsClosedKindAndHint covers the
// refusals that never reach dispatch. Those are exactly the ones an operator
// cannot otherwise see: a rejected caller class or an unknown method produces no
// task state, no event, and no audit record.
func TestBoundaryLogging_RecordsEveryRefusalWithItsClosedKindAndHint(t *testing.T) {
	for name, test := range map[string]struct {
		caller  CallerClass
		payload []byte
		kind    domain.ErrorCode
	}{
		"unusable envelope": {caller: CallerOperatorCLI, payload: []byte("{"), kind: domain.ErrorInvalidArgument},
		"unknown method": {
			caller:  CallerOperatorCLI,
			payload: request(t, Method("Invented"), "operation-log-0002"),
			kind:    domain.ErrorInvalidArgument,
		},
		"forbidden caller": {
			caller:  CallerMCPFacade,
			payload: request(t, MethodReadAudit, "operation-log-0003"),
			kind:    domain.ErrorUnauthorized,
		},
	} {
		t.Run(name, func(t *testing.T) {
			logger := &recordingLogger{}
			handler := loggingHandler(t, logger)
			outcome := handler.handle(context.Background(), test.caller, test.payload)
			if outcome.Status == domain.OperationCompleted {
				t.Fatalf("handle() completed, want a refusal")
			}
			if len(logger.records) != 1 {
				t.Fatalf("recorded %d boundary records, want exactly 1", len(logger.records))
			}
			record := logger.records[0]
			if record.Outcome != application.BoundaryFailed {
				t.Errorf("outcome = %q, want failed", record.Outcome)
			}
			if record.ErrorKind != test.kind {
				t.Errorf("error kind = %q, want %q", record.ErrorKind, test.kind)
			}
			if strings.TrimSpace(record.Hint) == "" {
				t.Errorf("a failure carried no operator hint: %#v", record)
			}
		})
	}
}

// TestBoundaryLogging_CarriesNoRequestContent is the guarantee that makes this
// safe to leave on. The record is a closed struct, so proving the boundary
// cannot carry a payload is a matter of what fields exist, not of reviewing
// every call site forever.
func TestBoundaryLogging_CarriesNoRequestContent(t *testing.T) {
	logger := &recordingLogger{}
	handler := loggingHandler(t, logger)
	payload, err := json.Marshal(Request{
		ProtocolVersion: ProtocolVersion, OperationID: "operation-log-0004",
		Method: MethodShowDecision, Payload: json.RawMessage(`{"taskHandle":"task-secret-objective"}`),
	})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	handler.handle(context.Background(), CallerOperatorCLI, payload)
	if len(logger.records) != 1 {
		t.Fatalf("recorded %d boundary records, want exactly 1", len(logger.records))
	}
	rendered, err := json.Marshal(logger.records[0])
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}
	if strings.Contains(string(rendered), "task-secret-objective") {
		t.Errorf("boundary record carried request content: %s", rendered)
	}
}

// TestBoundaryLogging_StaysOptional keeps an unwired handler serving. A missing
// logger is a deployment that records nothing, never a service that refuses
// work it could otherwise do.
func TestBoundaryLogging_StaysOptional(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queries: decisionQueriesFixture(), Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	if outcome := handler.handle(
		context.Background(), CallerOperatorCLI, request(t, MethodListDecisions, "operation-log-0005"),
	); outcome.Status != domain.OperationCompleted {
		t.Fatalf("handle() without a logger = %q, want completed", outcome.Status)
	}
}
