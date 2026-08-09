package mcpadapter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewAndRun_RejectInvalidCompositionAndTransport(t *testing.T) {
	valid := Config{
		Client: &fakeClient{}, ServiceInstanceID: "service-instance-0001",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	}
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "missing client", mutate: func(config *Config) { config.Client = nil }},
		{name: "missing operation source", mutate: func(config *Config) { config.NewOperationID = nil }},
		{name: "invalid service", mutate: func(config *Config) { config.ServiceInstanceID = "bad service" }},
		{name: "negative timeout", mutate: func(config *Config) { config.ReconcileTimeout = -time.Second }},
		{name: "excess timeout", mutate: func(config *Config) { config.ReconcileTimeout = maximumReconcileTimeout + time.Second }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if facade, err := New(config); err == nil || facade != nil {
				t.Fatalf("New() = %#v, %v, want closed error", facade, err)
			}
		})
	}
	facade, err := New(valid)
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 Boundary test proves nil contexts fail closed.
	if err := facade.Run(nil, failingTransport{}); err == nil {
		t.Fatal("Run(nil context) error = nil")
	}
	if err := facade.Run(context.Background(), nil); err == nil {
		t.Fatal("Run(nil transport) error = nil")
	}
	private := errors.New("private transport detail")
	if err := facade.Run(context.Background(), failingTransport{err: private}); !errors.Is(err, private) {
		t.Fatalf("Run(failing transport) error = %v", err)
	}
}

func TestFacade_RejectsInvalidArgumentsAndLocalResultsWithoutPrivateMetadata(t *testing.T) {
	client := &fakeClient{}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: "service-instance-0001",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	session := connectFacade(t, facade)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("prepare-0001", "service-instance-0001"), Name: ToolPrepareTask,
		Arguments: map[string]any{"shape": "scout", "authority": "forged"},
	})
	if err != nil || result == nil || !result.IsError || len(client.calls) != 0 {
		t.Fatalf("invalid arguments = %#v, %v, calls=%v", result, err, client.calls)
	}

	client.prepareResults = []localapi.PrepareTaskResult{{
		SchemaVersion: 1, OperationID: "prepare-0001", TaskHandle: "task-0001",
		State: domain.TaskPrepared, StateVersion: 1, SideEffect: localapi.SideEffectMutate,
		ManagedRun: application.ManagedRunPreparation{
			ExternalRunRef: "different-task", RegistrationNonce: "registration-nonce_private",
			ExpiresAt: time.Now().UTC().Add(time.Hour),
		},
	}}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: callMeta("prepare-0001", "service-instance-0001"), Name: ToolPrepareTask,
		Arguments: prepareInput(),
	})
	if err != nil || result == nil || !result.IsError || result.Meta[ManagedRunResultMetaKey] != nil {
		t.Fatalf("invalid local result = %#v, %v", result, err)
	}
}

func TestFacade_ReadAuthorizationErrorsCoverEveryHandler(t *testing.T) {
	facade, err := New(Config{
		Client: &fakeClient{}, ServiceInstanceID: "service-instance-0001",
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{}}
	if _, _, err := facade.getTask(context.Background(), request, TaskInput{TaskHandle: "task-0001"}); err == nil {
		t.Fatal("getTask(hostile) error = nil")
	}
	if _, _, err := facade.explainTask(context.Background(), request, TaskInput{TaskHandle: "task-0001"}); err == nil {
		t.Fatal("explainTask(hostile) error = nil")
	}
	if _, _, err := facade.prepareTask(context.Background(), request, prepareInput()); err == nil {
		t.Fatal("prepareTask(hostile) error = nil")
	}
	if _, err := facade.authorize(nil); err == nil {
		t.Fatal("authorize(nil) error = nil")
	}
	channelRequest := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{CallContextMetaKey: make(chan int)}}}
	if _, err := facade.authorize(channelRequest); err == nil {
		t.Fatal("authorize(unencodable) error = nil")
	}
}

func TestUncertainMutation_UsesOnlyRetryableClosedCodes(t *testing.T) {
	retryableUnavailable, _ := domain.NewFailure(domain.ErrorUnavailable, true, "unavailable", "reconcile", nil)
	nonretryableUnavailable, _ := domain.NewFailure(domain.ErrorUnavailable, false, "unavailable", "inspect", nil)
	retryableInvalid, _ := domain.NewFailure(domain.ErrorInvalidArgument, true, "invalid", "correct", nil)
	retryableDeadline, _ := domain.NewFailure(domain.ErrorDeadlineExceeded, true, "deadline", "reconcile", nil)
	retryableUnknown, _ := domain.NewFailure(domain.ErrorUnknown, true, "unknown", "reconcile", nil)
	tests := []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{name: "raw", ctx: context.Background(), err: errors.New("private"), want: false},
		{name: "unavailable", ctx: context.Background(), err: retryableUnavailable, want: true},
		{name: "nonretryable", ctx: context.Background(), err: nonretryableUnavailable, want: false},
		{name: "invalid", ctx: context.Background(), err: retryableInvalid, want: false},
		{name: "deadline", ctx: context.Background(), err: retryableDeadline, want: true},
		{name: "unknown", ctx: context.Background(), err: retryableUnknown, want: true},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	tests = append(tests, struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{name: "canceled request", ctx: canceled, err: errors.New("private"), want: true})
	for _, test := range tests {
		if got := uncertainMutation(test.ctx, test.err); got != test.want {
			t.Errorf("uncertainMutation(%s) = %v, want %v", test.name, got, test.want)
		}
	}
}

func TestReconcilePreparation_StopsOnEveryNonCompletedOutcome(t *testing.T) {
	original := errors.New("original uncertain result")
	validOperation := application.OperationView{
		SchemaVersion: 1, OperationID: "prepare-0001", Command: "PrepareTask",
		Status: domain.OperationAccepted, StateVersion: 1,
	}
	tests := []struct {
		name      string
		operation application.OperationView
		opErr     error
		newID     func() (string, error)
		wantCode  domain.ErrorCode
	}{
		{name: "operation source failure", operation: validOperation, newID: func() (string, error) { return "", errors.New("entropy") }},
		{name: "invalid operation source", operation: validOperation, newID: func() (string, error) { return "BAD ID", nil }},
		{name: "query failure", operation: validOperation, opErr: errors.New("disconnect")},
		{name: "identity mismatch", operation: func() application.OperationView {
			value := validOperation
			value.OperationID = "other-0001"
			return value
		}()},
		{name: "command mismatch", operation: func() application.OperationView { value := validOperation; value.Command = "Other"; return value }()},
		{name: "accepted", operation: validOperation},
		{name: "unknown", operation: func() application.OperationView {
			value := validOperation
			value.Status = domain.OperationUnknown
			return value
		}()},
		{name: "rejected invalid code", operation: func() application.OperationView {
			value := validOperation
			value.Status = domain.OperationRejected
			return value
		}()},
		{name: "rejected", operation: func() application.OperationView {
			value := validOperation
			value.Status = domain.OperationRejected
			value.ErrorCode = domain.ErrorConflict
			return value
		}(), wantCode: domain.ErrorConflict},
		{name: "invalid status", operation: func() application.OperationView { value := validOperation; value.Status = "invented"; return value }(), wantCode: domain.ErrorUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			newID := test.newID
			if newID == nil {
				newID = func() (string, error) { return "reconcile-0001", nil }
			}
			client := &fakeClient{operation: test.operation, operationError: test.opErr}
			facade, err := New(Config{
				Client: client, ServiceInstanceID: "service-instance-0001",
				NewOperationID: newID, ReconcileTimeout: time.Second,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, gotErr := facade.reconcilePreparation(context.Background(), "prepare-0001", prepareInput().local(), original)
			if test.wantCode == domain.ErrorConflict {
				var failure *domain.Failure
				if !errors.As(gotErr, &failure) || failure.Code != test.wantCode {
					t.Fatalf("error = %v, want %s", gotErr, test.wantCode)
				}
			} else if test.wantCode == domain.ErrorUnknown {
				if gotErr == nil || !strings.Contains(gotErr.Error(), "unknown operation") {
					t.Fatalf("error = %v, want unknown status", gotErr)
				}
			} else if !errors.Is(gotErr, original) {
				t.Fatalf("error = %v, want original", gotErr)
			}
		})
	}
	facade := &Facade{}
	//lint:ignore SA1012 Boundary test proves reconciliation rejects nil contexts.
	if _, err := facade.reconcilePreparation(nil, "prepare-0001", localapi.PrepareTaskInput{}, original); !errors.Is(err, original) {
		t.Fatalf("nil context error = %v, want original", err)
	}
}

func TestSafeFailure_InvalidDefinitionStaysGeneric(t *testing.T) {
	if err := safeFailure("invented", false, "message", "hint"); err == nil || strings.Contains(err.Error(), "invented") {
		t.Fatalf("safeFailure(invalid) = %v", err)
	}
}

type failingTransport struct{ err error }

func (transport failingTransport) Connect(context.Context) (mcp.Connection, error) {
	return nil, transport.err
}
