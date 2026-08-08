// Package localapi implements the canonical owner-only local service boundary.
package localapi

import (
	"context"
	"encoding/json"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	// ProtocolVersion identifies the strict local service contract.
	ProtocolVersion = "devcrew.local.v1"
	// MaxRequestBytes bounds one newline-delimited request before decoding.
	MaxRequestBytes = 64 * 1024
	// MaxResponseBytes bounds one newline-delimited response before decoding.
	MaxResponseBytes = 1024 * 1024
)

// CallerClass is derived from the protected endpoint, never from request content.
type CallerClass string

const (
	CallerOperatorCLI  CallerClass = "operator_cli"
	CallerMCPFacade    CallerClass = "mcp_facade"
	CallerWorkerReport CallerClass = "worker_report"
	CallerComisControl CallerClass = "comis_control"
)

func (caller CallerClass) valid() bool {
	switch caller {
	case CallerOperatorCLI, CallerMCPFacade, CallerWorkerReport, CallerComisControl:
		return true
	default:
		return false
	}
}

// Method is the closed read-only method catalog available before mutation binding.
type Method string

const (
	MethodDiagnose    Method = "Diagnose"
	MethodFleet       Method = "FleetStatus"
	MethodListTasks   Method = "ListTasks"
	MethodShowTask    Method = "ShowTask"
	MethodExplainTask Method = "ExplainTask"
	MethodOperation   Method = "GetOperation"
)

func (method Method) valid() bool {
	switch method {
	case MethodDiagnose, MethodFleet, MethodListTasks, MethodShowTask, MethodExplainTask, MethodOperation:
		return true
	default:
		return false
	}
}

// Request is one strict newline-delimited local service request.
type Request struct {
	ProtocolVersion string          `json:"protocolVersion"`
	OperationID     string          `json:"operationId"`
	Method          Method          `json:"method"`
	Payload         json.RawMessage `json:"payload"`
	DeadlineAtMs    *int64          `json:"deadlineAtMs,omitempty"`
}

// WireError is safe to return to a local client.
type WireError struct {
	Code      domain.ErrorCode `json:"code"`
	Message   string           `json:"message"`
	Retryable bool             `json:"retryable"`
	Hint      string           `json:"hint"`
}

// Outcome is one strict local service result.
type Outcome struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	OperationID     string                 `json:"operationId"`
	Status          domain.OperationStatus `json:"status"`
	StateVersion    *int64                 `json:"stateVersion,omitempty"`
	Result          json.RawMessage        `json:"result,omitempty"`
	Error           *WireError             `json:"error,omitempty"`
}

// ReadQueries is the narrow application surface consumed by the local boundary.
type ReadQueries interface {
	Diagnose(context.Context) (application.DiagnosticReport, error)
	Fleet(context.Context) (application.FleetSnapshot, error)
	ListTasks(context.Context) (application.TaskList, error)
	ShowTask(context.Context, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string) (application.TaskExplanation, error)
	Operation(context.Context, string) (application.OperationView, error)
}

type emptyPayload struct{}

type taskPayload struct {
	TaskHandle string `json:"taskHandle"`
}

type operationPayload struct {
	OperationID string `json:"operationId"`
}
