package localapi

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type apiMutations struct {
	command application.PrepareTaskCommand
	result  application.MutationResult
	err     error
}

type apiInterventions struct {
	command application.HandbackTaskCommand
	result  application.MutationResult
	err     error
}

type apiCleanup struct {
	command application.CleanupTaskCommand
	result  application.MutationResult
	err     error
}

func TestDecodePrepareTaskInput_UsesStrictBoundedPayload(t *testing.T) {
	input, err := DecodePrepareTaskInput([]byte(`{
        "shape":"scout","repositoryId":"product-api",
        "baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "acceptanceCriteria":["Return one bounded report."],
        "constraints":["Do not deliver."],"validationProfile":"go-default",
        "deliveryMode":"report","workerProfileId":"fixture-worker"
    }`))
	if err != nil || input.Shape != domain.ShapeScout || input.RepositoryID != "product-api" {
		t.Fatalf("DecodePrepareTaskInput() = %#v, %v", input, err)
	}
	if _, err := DecodePrepareTaskInput(nil); err == nil {
		t.Fatal("DecodePrepareTaskInput(empty) error = nil")
	}
	if _, err := DecodePrepareTaskInput([]byte(`{"shape":"scout","unexpected":true}`)); err == nil {
		t.Fatal("DecodePrepareTaskInput(unknown field) error = nil")
	}
}

func (cleanup *apiCleanup) CleanupTask(
	_ context.Context,
	command application.CleanupTaskCommand,
) (application.MutationResult, error) {
	cleanup.command = command
	return cleanup.result, cleanup.err
}

func (interventions *apiInterventions) HandbackTask(
	_ context.Context,
	command application.HandbackTaskCommand,
) (application.MutationResult, error) {
	interventions.command = command
	return interventions.result, interventions.err
}

func (mutations *apiMutations) PrepareTask(_ context.Context, command application.PrepareTaskCommand) (application.MutationResult, error) {
	mutations.command = command
	return mutations.result, mutations.err
}

func TestServerClient_PrepareTaskUsesCanonicalMutationAndPrivateRegistration(t *testing.T) {
	now := time.Now().UTC()
	preparation := application.ManagedRunPreparation{
		ExternalRunRef: "task-0001", RegistrationNonce: "registration-nonce_localapi",
		RequestedAttachment: application.PreparedRuntimeAttachment{Kind: application.RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/task-0001/attachment.sock"},
		ExpiresAt:           now.Add(time.Hour), State: application.PreparationOpen,
	}
	mutations := &apiMutations{result: application.MutationResult{
		Task:        domain.Task{Handle: "task-0001", State: domain.TaskPrepared, StateVersion: 8},
		Operation:   domain.OperationRecord{ID: "operation-prepare-local", Status: domain.OperationCompleted, StateVersion: 8},
		Preparation: &preparation,
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: mutations,
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	input := PrepareTaskInput{
		Shape: domain.ShapeScout, RepositoryID: "product-api",
		BaseRevision:       "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AcceptanceCriteria: []string{"Return one bounded report."}, Constraints: []string{"Do not deliver."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryReport, WorkerProfileID: "fixture-worker",
	}
	result, err := client.PrepareTask(context.Background(), "operation-prepare-local", input)
	if err != nil {
		t.Fatalf("PrepareTask() error = %v", err)
	}
	if result.TaskHandle != "task-0001" || result.State != domain.TaskPrepared ||
		result.StateVersion != 8 || result.SideEffect != SideEffectMutate ||
		!reflect.DeepEqual(result.ManagedRun, preparation) {
		t.Fatalf("PrepareTask() = %#v", result)
	}
	if mutations.command.OperationID != "operation-prepare-local" ||
		mutations.command.ServiceInstanceID != "service-instance_a" ||
		mutations.command.RepositoryID != input.RepositoryID ||
		mutations.command.Shape != input.Shape {
		t.Fatalf("canonical mutation command = %#v", mutations.command)
	}
}

func TestServerClient_HandbackUsesCanonicalRevalidationMutation(t *testing.T) {
	interventions := &apiInterventions{result: application.MutationResult{
		Task: domain.Task{Handle: "task-handback-local", State: domain.TaskValidating, StateVersion: 21},
		Operation: domain.OperationRecord{
			ID: "operation-handback-local", Status: domain.OperationCompleted, StateVersion: 21,
		},
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Interventions: interventions, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := HandbackTaskInput{
		TaskHandle: "task-handback-local", Action: application.HandbackValidateDeveloperWork,
	}
	result, err := client.HandbackTask(context.Background(), "operation-handback-local", input)
	if err != nil {
		t.Fatalf("HandbackTask() error = %v", err)
	}
	if result.TaskHandle != input.TaskHandle || result.State != domain.TaskValidating ||
		result.StateVersion != 21 || result.SideEffect != SideEffectMutate {
		t.Fatalf("HandbackTask() = %#v", result)
	}
	if interventions.command.OperationID != "operation-handback-local" ||
		interventions.command.TaskHandle != input.TaskHandle || interventions.command.Action != input.Action {
		t.Fatalf("canonical handback command = %#v", interventions.command)
	}
}

func TestServerClient_CleanupUsesCanonicalReleaseBeforeRemovalMutation(t *testing.T) {
	cleanup := &apiCleanup{result: application.MutationResult{
		Task: domain.Task{Handle: "task-cleanup-local", State: domain.TaskCleaned, StateVersion: 34},
		Operation: domain.OperationRecord{
			ID: "operation-cleanup-local", Status: domain.OperationCompleted, StateVersion: 34,
		},
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Cleanup: cleanup, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.CleanupTask(context.Background(), "operation-cleanup-local", CleanupTaskInput{
		TaskHandle: "task-cleanup-local",
	})
	if err != nil {
		t.Fatalf("CleanupTask() error = %v", err)
	}
	if result.TaskHandle != "task-cleanup-local" || result.State != domain.TaskCleaned ||
		result.StateVersion != 34 || result.SideEffect != SideEffectMutate {
		t.Fatalf("CleanupTask() = %#v", result)
	}
	if cleanup.command.OperationID != "operation-cleanup-local" || cleanup.command.TaskHandle != "task-cleanup-local" {
		t.Fatalf("canonical cleanup command = %#v", cleanup.command)
	}
}

func TestPrepareTaskCallerAndPayloadCannotSelectAuthority(t *testing.T) {
	now := time.Date(2026, time.August, 9, 20, 0, 0, 0, time.UTC)
	mutations := &apiMutations{}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: mutations,
		ServiceInstanceID: "service-instance_a", Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := `{"protocolVersion":"devcrew.local.v1","operationId":"operation-prepare-local","method":"PrepareTask","payload":{"shape":"scout","repositoryId":"product-api","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Return one report."],"constraints":[],"validationProfile":"go-default","deliveryMode":"report","workerProfileId":"fixture-worker","serviceInstanceId":"forged-instance"}}`
	for _, caller := range []CallerClass{CallerMCPFacade, CallerWorkerReport, CallerComisControl} {
		outcome := handler.handle(context.Background(), caller, []byte(request))
		if outcome.Status != domain.OperationRejected || outcome.Error == nil {
			t.Fatalf("caller %q outcome = %#v", caller, outcome)
		}
	}
	if mutations.command.OperationID != "" {
		t.Fatalf("forged payload reached mutations: %#v", mutations.command)
	}
}

func TestPrepareTaskDefensiveConfigurationAndIncompleteOutcome(t *testing.T) {
	if _, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: &apiMutations{},
		ServiceInstanceID: "bad instance", Clock: time.Now,
	}); err == nil {
		t.Fatal("NewHandler(invalid service instance) error = nil")
	}
	readOnly, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"protocolVersion":"devcrew.local.v1","operationId":"operation-prepare-local","method":"PrepareTask","payload":{"shape":"scout","repositoryId":"product-api","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Return one report."],"constraints":[],"validationProfile":"go-default","deliveryMode":"report","workerProfileId":"fixture-worker"}}`)
	if outcome := readOnly.handle(context.Background(), CallerMCPFacade, payload); outcome.Error == nil || outcome.Error.Code != domain.ErrorUnavailable {
		t.Fatalf("read-only prepare outcome = %#v", outcome)
	}
	incomplete, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Mutations: &apiMutations{result: application.MutationResult{}},
		ServiceInstanceID: "service-instance_a", Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome := incomplete.handle(context.Background(), CallerOperatorCLI, payload); outcome.Error == nil || outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("incomplete prepare outcome = %#v", outcome)
	}
	if MethodListTasks.SideEffect() != SideEffectRead || MethodGetLaunchPlan.SideEffect() != SideEffectRead || MethodPrepareTask.SideEffect() != SideEffectMutate {
		t.Fatal("method side-effect classification drifted")
	}
}

func startHandlerServer(t *testing.T, handler *Handler, caller CallerClass) string {
	t.Helper()
	directory, err := os.MkdirTemp("/private/tmp", "dc-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	socketPath := filepath.Join(directory, "mutation.sock")
	server, err := Listen(socketPath, caller, handler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Close() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})
	return socketPath
}
