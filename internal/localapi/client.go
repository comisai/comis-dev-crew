package localapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// Client is the typed canonical local service client used by CLI and MCP adapters.
type Client struct {
	socketPath string
	timeout    time.Duration
}

// NewClient validates the fixed endpoint and default request timeout.
func NewClient(socketPath string, timeout time.Duration) (*Client, error) {
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath {
		return nil, errors.New("create local API client: socket path must be absolute and canonical")
	}
	if len(socketPath) > maximumSocketPath {
		return nil, errors.New("create local API client: socket path is too long")
	}
	if timeout <= 0 {
		return nil, errors.New("create local API client: timeout must be positive")
	}
	return &Client{socketPath: socketPath, timeout: timeout}, nil
}

// Diagnose reads bounded service readiness.
func (client *Client) Diagnose(ctx context.Context, operationID string) (application.DiagnosticReport, error) {
	var result application.DiagnosticReport
	err := client.call(ctx, operationID, MethodDiagnose, emptyPayload{}, &result)
	return result, err
}

// Fleet reads the canonical fleet snapshot.
func (client *Client) Fleet(ctx context.Context, operationID string) (application.FleetSnapshot, error) {
	var result application.FleetSnapshot
	err := client.call(ctx, operationID, MethodFleet, emptyPayload{}, &result)
	return result, err
}

// ListTasks reads the canonical task list.
func (client *Client) ListTasks(ctx context.Context, operationID string) (application.TaskList, error) {
	var result application.TaskList
	err := client.call(ctx, operationID, MethodListTasks, emptyPayload{}, &result)
	return result, err
}

// ListWorkerProfiles reads the reviewed dispatch catalog and its posture.
func (client *Client) ListWorkerProfiles(
	ctx context.Context,
	operationID string,
) (application.WorkerProfileList, error) {
	var result application.WorkerProfileList
	err := client.call(ctx, operationID, MethodWorkerProfiles, emptyPayload{}, &result)
	return result, err
}

// ShowTask reads durable detail for one task handle.
func (client *Client) ShowTask(ctx context.Context, operationID, taskHandle string) (application.TaskDetail, error) {
	var result application.TaskDetail
	err := client.call(ctx, operationID, MethodShowTask, taskPayload{TaskHandle: taskHandle}, &result)
	return result, err
}

// ExplainTask reads the canonical reasoned task posture.
func (client *Client) ExplainTask(ctx context.Context, operationID, taskHandle string) (application.TaskExplanation, error) {
	var result application.TaskExplanation
	err := client.call(ctx, operationID, MethodExplainTask, taskPayload{TaskHandle: taskHandle}, &result)
	return result, err
}

// GetLaunchPlan reads the safe reviewed launch requirements for one task.
func (client *Client) GetLaunchPlan(ctx context.Context, operationID, taskHandle string) (application.LaunchPlan, error) {
	var result application.LaunchPlan
	err := client.call(ctx, operationID, MethodGetLaunchPlan, taskPayload{TaskHandle: taskHandle}, &result)
	return result, err
}

// Operation reconciles one stable durable operation ID.
func (client *Client) Operation(ctx context.Context, operationID, targetOperationID string) (application.OperationView, error) {
	var result application.OperationView
	err := client.call(ctx, operationID, MethodOperation, operationPayload{OperationID: targetOperationID}, &result)
	return result, err
}

// PrepareTask executes one canonical idempotent task preparation.
func (client *Client) PrepareTask(ctx context.Context, operationID string, input PrepareTaskInput) (PrepareTaskResult, error) {
	var result PrepareTaskResult
	err := client.call(ctx, operationID, MethodPrepareTask, input, &result)
	return result, err
}

// ReconcileTask executes one canonical idempotent unknown-task recovery.
func (client *Client) ReconcileTask(ctx context.Context, operationID string, input ReconcileTaskInput) (TaskMutationResult, error) {
	var result TaskMutationResult
	err := client.call(ctx, operationID, MethodReconcileTask, input, &result)
	return result, err
}

// HandbackTask executes one canonical idempotent paused-worktree handback.
func (client *Client) HandbackTask(ctx context.Context, operationID string, input HandbackTaskInput) (TaskMutationResult, error) {
	var result TaskMutationResult
	err := client.call(ctx, operationID, MethodHandbackTask, input, &result)
	return result, err
}

// CleanupTask executes one canonical idempotent release-before-removal cleanup.
func (client *Client) CleanupTask(ctx context.Context, operationID string, input CleanupTaskInput) (TaskMutationResult, error) {
	var result TaskMutationResult
	err := client.call(ctx, operationID, MethodCleanupTask, input, &result)
	return result, err
}

// PauseTask asks one task's worker to reach a safe boundary.
func (client *Client) PauseTask(ctx context.Context, operationID string, input PauseTaskInput) (TaskMutationResult, error) {
	var result TaskMutationResult
	err := client.call(ctx, operationID, MethodPauseTask, input, &result)
	return result, err
}

// CancelTask stops one task while preserving its artifacts.
func (client *Client) CancelTask(ctx context.Context, operationID string, input CancelTaskInput) (TaskMutationResult, error) {
	var result TaskMutationResult
	err := client.call(ctx, operationID, MethodCancelTask, input, &result)
	return result, err
}

func (client *Client) call(ctx context.Context, operationID string, method Method, payload any, result any) error {
	if ctx == nil {
		return errors.New("call local API: context is required")
	}
	if err := domain.ValidateOperationID(operationID); err != nil {
		return fmt.Errorf("call local API: %w", err)
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode local API payload: %w", err)
	}
	request := Request{
		ProtocolVersion: ProtocolVersion,
		OperationID:     operationID,
		Method:          method,
		Payload:         encodedPayload,
	}
	callContext, cancel := context.WithTimeout(ctx, client.timeout)
	defer cancel()
	if deadline, ok := callContext.Deadline(); ok {
		deadlineMilliseconds := deadline.UnixMilli()
		request.DeadlineAtMs = &deadlineMilliseconds
	}
	outcome, err := client.exchange(callContext, request)
	if err != nil {
		return localTransportFailure(err)
	}
	if outcome.ProtocolVersion != ProtocolVersion || outcome.OperationID != operationID {
		return errors.New("local API response identity mismatch")
	}
	switch outcome.Status {
	case domain.OperationCompleted:
		if outcome.Error != nil || outcome.StateVersion == nil || len(outcome.Result) == 0 {
			return errors.New("local API completed response is incomplete")
		}
		if err := decodeObject(outcome.Result, result); err != nil {
			return fmt.Errorf("decode local API result: %w", err)
		}
		resultVersion, ok := projectedStateVersion(result)
		if !ok || resultVersion != *outcome.StateVersion {
			return errors.New("local API response state version mismatch")
		}
		return nil
	case domain.OperationRejected:
		if outcome.Error == nil {
			return errors.New("local API rejected response has no error")
		}
		failure, err := domain.NewFailure(outcome.Error.Code, outcome.Error.Retryable, outcome.Error.Message, outcome.Error.Hint, nil)
		if err != nil {
			return fmt.Errorf("validate local API error: %w", err)
		}
		return failure
	case domain.OperationAccepted, domain.OperationUnknown:
		failure, err := domain.NewFailure(domain.ErrorUnknown, true, "operation outcome is uncertain", "query the stable operation ID before retrying", nil)
		if err != nil {
			return fmt.Errorf("construct uncertain local API error: %w", err)
		}
		return failure
	default:
		return errors.New("local API response has unknown status")
	}
}

func projectedStateVersion(result any) (int64, bool) {
	switch projection := result.(type) {
	case *application.DiagnosticReport:
		return projection.StateVersion, true
	case *application.FleetSnapshot:
		return projection.StateVersion, true
	case *application.TaskList:
		return projection.StateVersion, true
	case *application.WorkerProfileList:
		return projection.StateVersion, true
	case *application.TaskDetail:
		return projection.StateVersion, true
	case *application.TaskExplanation:
		return projection.Summary.StateVersion, true
	case *application.LaunchPlan:
		return projection.StateVersion, true
	case *application.OperationView:
		return projection.StateVersion, true
	case *PrepareTaskResult:
		return projection.StateVersion, true
	case *TaskMutationResult:
		return projection.StateVersion, true
	default:
		return 0, false
	}
}

func localTransportFailure(cause error) error {
	failure, err := domain.NewFailure(
		domain.ErrorUnavailable,
		true,
		"local service is unavailable",
		"verify the service socket and retry",
		cause,
	)
	if err != nil {
		return fmt.Errorf("construct local transport failure: %w", err)
	}
	return failure
}

func (client *Client) exchange(ctx context.Context, request Request) (outcome Outcome, resultErr error) {
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", client.socketPath)
	if err != nil {
		return Outcome{}, fmt.Errorf("connect to local API: %w", err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close local API connection: %w", err))
		}
	}()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return Outcome{}, fmt.Errorf("set local API deadline: %w", err)
		}
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return Outcome{}, fmt.Errorf("encode local API request: %w", err)
	}
	encoded = append(encoded, '\n')
	if _, err := io.Copy(connection, bytes.NewReader(encoded)); err != nil {
		return Outcome{}, fmt.Errorf("write local API request: %w", err)
	}
	reader := bufio.NewReaderSize(connection, MaxResponseBytes+1)
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > MaxResponseBytes {
		return Outcome{}, errors.New("local API response exceeds size limit")
	}
	if err != nil {
		return Outcome{}, fmt.Errorf("read local API response: %w", err)
	}
	if err := decodeObject(line[:len(line)-1], &outcome); err != nil {
		return Outcome{}, fmt.Errorf("decode local API outcome: %w", err)
	}
	return outcome, nil
}
