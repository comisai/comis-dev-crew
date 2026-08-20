package application

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIntegrationReservesPolicyBoundCandidateBeforeApplying(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	command := integrationCommand()
	reserved := integrationReservation(command, IntegrationCherryPick)
	store := &integrationStore{
		policyID:    "integration-reviewed",
		reservation: reserved,
		completed:   integrationResult(reserved, IntegrationApplied, strings.Repeat("c", 40), nil, at),
	}
	adapter := &integrationAdapter{result: IntegrationAdapterResult{
		Outcome: IntegrationApplied, PreviousHead: command.ExpectedIntegrationHead,
		ResultingHead: strings.Repeat("c", 40),
	}}
	integrations, err := NewIntegrations(IntegrationConfig{
		Store: store, Adapter: adapter,
		Policies: func(policyID string) (IntegrationStrategy, error) {
			if policyID != "integration-reviewed" {
				return "", errors.New("unexpected policy")
			}
			return IntegrationCherryPick, nil
		},
		Clock: func() time.Time { return at },
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := integrations.ApplyCandidate(context.Background(), command)
	if err != nil {
		t.Fatalf("ApplyCandidate() error = %v", err)
	}
	if !reflect.DeepEqual(result, store.completed) {
		t.Fatalf("result = %#v, want %#v", result, store.completed)
	}
	if store.sequence != "policy,reserve,complete" {
		t.Fatalf("store sequence = %q", store.sequence)
	}
	if len(adapter.requests) != 1 || adapter.requests[0] != reserved.AdapterRequest() {
		t.Fatalf("adapter requests = %#v", adapter.requests)
	}
	if !reflect.DeepEqual(store.completion.AdapterResult, adapter.result) || store.completion.At != at {
		t.Fatalf("completion = %#v", store.completion)
	}
	if store.request.SubjectDigest == "" || store.request.Strategy != IntegrationCherryPick ||
		store.request.PolicyID != "integration-reviewed" || store.request.Command != command {
		t.Fatalf("reservation request = %#v", store.request)
	}
}

func TestIntegrationReplaysWithoutReapplyingCandidate(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	command := integrationCommand()
	reserved := integrationReservation(command, IntegrationMerge)
	replayed := integrationResult(reserved, IntegrationApplied, strings.Repeat("d", 40), nil, at)
	reserved.Result = &replayed
	store := &integrationStore{policyID: "integration-reviewed", reservation: reserved}
	adapter := &integrationAdapter{}
	integrations, err := NewIntegrations(IntegrationConfig{
		Store: store, Adapter: adapter,
		Policies: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil },
		Clock:    func() time.Time { return at },
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := integrations.ApplyCandidate(context.Background(), command)
	if err != nil || !reflect.DeepEqual(result, replayed) {
		t.Fatalf("ApplyCandidate(replay) = %#v, %v", result, err)
	}
	if len(adapter.requests) != 0 || store.sequence != "policy,reserve" {
		t.Fatalf("replay crossed mutation boundary: requests=%d sequence=%q", len(adapter.requests), store.sequence)
	}
}

func TestIntegrationPersistsTypedConflictsWithoutClaimingAHead(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	command := integrationCommand()
	reserved := integrationReservation(command, IntegrationRebase)
	conflicts := []string{"internal/api.go", "web/client.ts"}
	store := &integrationStore{
		policyID: "integration-reviewed", reservation: reserved,
		completed: integrationResult(reserved, IntegrationConflicted, "", conflicts, at),
	}
	adapter := &integrationAdapter{result: IntegrationAdapterResult{
		Outcome: IntegrationConflicted, PreviousHead: command.ExpectedIntegrationHead,
		ConflictPaths: conflicts,
	}}
	integrations, err := NewIntegrations(IntegrationConfig{
		Store: store, Adapter: adapter,
		Policies: func(string) (IntegrationStrategy, error) { return IntegrationRebase, nil },
		Clock:    func() time.Time { return at },
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := integrations.ApplyCandidate(context.Background(), command)
	if err != nil {
		t.Fatalf("ApplyCandidate(conflict) error = %v", err)
	}
	if result.Outcome != IntegrationConflicted || result.ResultingHead != "" ||
		!reflect.DeepEqual(result.ConflictPaths, conflicts) {
		t.Fatalf("conflict result = %#v", result)
	}
}

func TestIntegrationRefusesInvalidOrUntrustedBoundaryResults(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	command := integrationCommand()
	validReservation := integrationReservation(command, IntegrationMerge)
	tests := []struct {
		name     string
		command  ApplyIntegrationCandidateCommand
		strategy IntegrationStrategy
		result   IntegrationAdapterResult
	}{
		{name: "invalid command", command: ApplyIntegrationCandidateCommand{}, strategy: IntegrationMerge},
		{name: "unknown strategy", command: command, strategy: "shell_fragment"},
		{name: "changed previous head", command: command, strategy: IntegrationMerge, result: IntegrationAdapterResult{
			Outcome: IntegrationApplied, PreviousHead: strings.Repeat("9", 40), ResultingHead: strings.Repeat("c", 40),
		}},
		{name: "applied without result head", command: command, strategy: IntegrationMerge, result: IntegrationAdapterResult{
			Outcome: IntegrationApplied, PreviousHead: command.ExpectedIntegrationHead,
		}},
		{name: "conflict with result head", command: command, strategy: IntegrationMerge, result: IntegrationAdapterResult{
			Outcome: IntegrationConflicted, PreviousHead: command.ExpectedIntegrationHead,
			ResultingHead: strings.Repeat("c", 40), ConflictPaths: []string{"conflict.txt"},
		}},
		{name: "conflict path traversal", command: command, strategy: IntegrationMerge, result: IntegrationAdapterResult{
			Outcome: IntegrationConflicted, PreviousHead: command.ExpectedIntegrationHead,
			ConflictPaths: []string{"../outside"},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := &integrationStore{policyID: "integration-reviewed", reservation: validReservation}
			adapter := &integrationAdapter{result: test.result}
			integrations, err := NewIntegrations(IntegrationConfig{
				Store: store, Adapter: adapter,
				Policies: func(string) (IntegrationStrategy, error) { return test.strategy, nil },
				Clock:    func() time.Time { return at },
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := integrations.ApplyCandidate(context.Background(), test.command); err == nil {
				t.Fatal("ApplyCandidate() error = nil")
			}
			if store.sequence == "policy,reserve,complete" {
				t.Fatal("invalid boundary result was committed")
			}
		})
	}
}

func integrationCommand() ApplyIntegrationCandidateCommand {
	return ApplyIntegrationCandidateCommand{
		OperationID: "integration-operation-0001", InitiativeHandle: "initiative-alpha",
		IntegrationTaskHandle: "task-integration", CandidateTaskHandle: "task-component",
		CandidateHead: strings.Repeat("b", 40), ExpectedIntegrationHead: strings.Repeat("a", 40),
	}
}

func integrationReservation(command ApplyIntegrationCandidateCommand, strategy IntegrationStrategy) ReservedIntegrationApplication {
	return ReservedIntegrationApplication{
		OperationID: command.OperationID, SubjectDigest: strings.Repeat("1", 64),
		InitiativeHandle: command.InitiativeHandle, IntegrationTaskHandle: command.IntegrationTaskHandle,
		PolicyID: "integration-reviewed", Strategy: strategy,
		Target: IntegrationTargetReference{
			RepositoryID: "product-api", WorktreePath: "/approved/worktrees/task-integration",
			ExpectedHead: command.ExpectedIntegrationHead,
		},
		Candidate: IntegrationCandidateReference{
			TaskHandle: command.CandidateTaskHandle, RepositoryID: "product-api",
			WorktreePath: "/approved/worktrees/task-component", BaseRevision: strings.Repeat("0", 40),
			HeadRevision: command.CandidateHead,
		},
	}
}

func integrationResult(
	reserved ReservedIntegrationApplication,
	outcome IntegrationOutcome,
	resultingHead string,
	conflicts []string,
	at time.Time,
) IntegrationApplicationResult {
	return IntegrationApplicationResult{
		OperationID: reserved.OperationID, InitiativeHandle: reserved.InitiativeHandle,
		IntegrationTaskHandle: reserved.IntegrationTaskHandle, Candidate: reserved.Candidate,
		Strategy: reserved.Strategy, Outcome: outcome, PreviousHead: reserved.Target.ExpectedHead,
		ResultingHead: resultingHead, ConflictPaths: conflicts, StateVersion: 17, CompletedAt: at,
	}
}

type integrationStore struct {
	policyID    string
	policyErr   error
	request     IntegrationReservationRequest
	reservation ReservedIntegrationApplication
	reserveErr  error
	completion  IntegrationCompletion
	completed   IntegrationApplicationResult
	completeErr error
	sequence    string
}

func (store *integrationStore) IntegrationPolicy(context.Context, string) (string, error) {
	store.append("policy")
	return store.policyID, store.policyErr
}

func (store *integrationStore) ReserveIntegrationApplication(
	_ context.Context,
	request IntegrationReservationRequest,
) (ReservedIntegrationApplication, error) {
	store.append("reserve")
	store.request = request
	store.reservation.SubjectDigest = request.SubjectDigest
	return store.reservation, store.reserveErr
}

func (store *integrationStore) CompleteIntegrationApplication(
	_ context.Context,
	completion IntegrationCompletion,
) (IntegrationApplicationResult, error) {
	store.append("complete")
	store.completion = completion
	return store.completed, store.completeErr
}

func (store *integrationStore) append(step string) {
	if store.sequence != "" {
		store.sequence += ","
	}
	store.sequence += step
}

type integrationAdapter struct {
	requests []IntegrationAdapterRequest
	result   IntegrationAdapterResult
	err      error
}

func (adapter *integrationAdapter) ApplyIntegrationCandidate(
	_ context.Context,
	request IntegrationAdapterRequest,
) (IntegrationAdapterResult, error) {
	adapter.requests = append(adapter.requests, request)
	return adapter.result, adapter.err
}
