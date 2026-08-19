package localapi

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type stubScoutReviews struct {
	calls  []string
	result application.MutationResult
	err    error
}

func (stub *stubScoutReviews) AttestScoutDecisions(
	_ context.Context,
	command application.AttestScoutDecisionsCommand,
) (application.MutationResult, error) {
	keys := ""
	for index, key := range command.OpenDecisionKeys {
		if index > 0 {
			keys += ","
		}
		keys += key
	}
	stub.calls = append(stub.calls, command.OperationID+":"+command.TaskHandle+":"+string(command.Finding)+":"+keys)
	return stub.result, stub.err
}

func newAttestClient(t *testing.T, reviews ScoutReviewAttestation) *Client {
	t.Helper()
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, ScoutReviews: reviews, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := filepath.Join(canonicalTempDir(t), "runtime", "devcrew.sock")
	server, err := Listen(socketPath, CallerOperatorCLI, handler)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := server.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("Server.Close() error = %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Server.Serve() error = %v", err)
		}
	})
	client, err := NewClient(socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

// The stated finding and its keys survive the round trip exactly. A transport
// that dropped either would turn an inventory naming open questions into one
// that cleared the surface.
func TestLocalAPI_AttestationCarriesTheStatedInventory(t *testing.T) {
	reviews := &stubScoutReviews{result: attestationMutationResult()}
	client := newAttestClient(t, reviews)

	result, err := client.AttestScoutDecisions(context.Background(), "operation-attest-0001", AttestScoutDecisionsInput{
		TaskHandle: "task-attest-0001", Finding: application.ScoutAttestationOpenDecisions,
		OpenDecisionKeys: []string{"schema-choice", "rollout-window"},
	})
	if err != nil {
		t.Fatalf("AttestScoutDecisions() error = %v", err)
	}
	if result.TaskHandle != "task-attest-0001" || result.SideEffect != SideEffectMutate {
		t.Fatalf("result = %#v", result)
	}
	want := "operation-attest-0001:task-attest-0001:open_decisions:schema-choice,rollout-window"
	if len(reviews.calls) != 1 || reviews.calls[0] != want {
		t.Fatalf("canonical calls = %v, want %q", reviews.calls, want)
	}
}

// A deployment without the attestation surface reports it unavailable rather
// than silently accepting an inventory nothing recorded.
func TestLocalAPI_AttestationReportsAnAbsentSurface(t *testing.T) {
	client := newAttestClient(t, nil)

	if _, err := client.AttestScoutDecisions(context.Background(), "operation-attest-0001", AttestScoutDecisionsInput{
		TaskHandle: "task-attest-0001", Finding: application.ScoutAttestationNoOpenDecisions,
	}); err == nil {
		t.Fatal("AttestScoutDecisions(absent surface) error = nil")
	}
}

// A refused inventory travels as a refusal. Reporting it as recorded would let
// cleanup proceed on an attestation the coordinator never accepted.
func TestLocalAPI_AttestationSurfacesACoordinatorRefusal(t *testing.T) {
	client := newAttestClient(t, &stubScoutReviews{err: &domain.Failure{
		Code: domain.ErrorInvalidArgument, Retryable: false, Message: "attestation states no closed finding",
	}})

	if _, err := client.AttestScoutDecisions(context.Background(), "operation-attest-0001", AttestScoutDecisionsInput{
		TaskHandle: "task-attest-0001",
	}); err == nil {
		t.Fatal("AttestScoutDecisions(refused) error = nil")
	}
}

func TestLocalAPI_AttestationIsAClassifiedMutation(t *testing.T) {
	if !MethodAttestScout.valid() || MethodAttestScout.SideEffect() != SideEffectMutate {
		t.Fatalf("MethodAttestScout classification = %q", MethodAttestScout.SideEffect())
	}
}

func attestationMutationResult() application.MutationResult {
	task := domain.Task{Handle: "task-attest-0001", StateVersion: 9, State: domain.TaskWorking}
	return application.MutationResult{
		Task: task,
		Operation: domain.OperationRecord{
			ID: "operation-attest-0001", Command: string(MethodAttestScout),
			Status: domain.OperationCompleted, ResultRef: task.Handle, StateVersion: 9,
		},
	}
}
