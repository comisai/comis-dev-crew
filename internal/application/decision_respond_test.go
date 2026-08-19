package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func respondMutations(t *testing.T, store *mutationStore) *Mutations {
	t.Helper()
	clock := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	mutations, err := NewMutations(MutationConfig{
		Store: store, Repositories: &repositoryCatalog{}, Workspaces: testWorkspacePreparer(),
		RuntimeAttachments: testRuntimeAttachments(),
		WorkerProfiles:     acceptingWorkerProfile, ValidationProfiles: acceptingValidationProfile,
		TaskIDs:            func(string) (string, error) { return "task-unused", nil },
		RegistrationNonces: testRegistrationNonceSource, PreparationTTL: time.Hour,
		Clock: func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutations
}

// An answer reaches the durable layer only once every part of it is a value the
// service can stand behind. The reply is worker-visible text, so it is checked
// here rather than at the surface that happened to carry it.
func TestMutations_RespondDecisionValidatesTheAnswerBeforeCommitting(t *testing.T) {
	valid := RespondDecisionCommand{
		OperationID: "operation-respond-0001", TaskHandle: "task-0001",
		ExternalKey: "schema-choice", Response: "use the versioned schema",
	}

	store := &mutationStore{}
	if _, err := respondMutations(t, store).RespondDecision(context.Background(), valid); err != nil {
		t.Fatalf("RespondDecision() error = %v", err)
	}
	if store.respondDecision.ExternalKey != "schema-choice" ||
		store.respondDecision.Response != "use the versioned schema" ||
		store.respondDecision.TaskHandle != "task-0001" {
		t.Fatalf("committed answer = %#v", store.respondDecision)
	}
	if store.respondDecision.At.IsZero() {
		t.Error("the committed answer carries no time")
	}

	for name, command := range map[string]RespondDecisionCommand{
		"unknown key shape": {OperationID: valid.OperationID, TaskHandle: valid.TaskHandle, ExternalKey: "not a key", Response: valid.Response},
		"empty answer":      {OperationID: valid.OperationID, TaskHandle: valid.TaskHandle, ExternalKey: valid.ExternalKey, Response: ""},
		"control sequence":  {OperationID: valid.OperationID, TaskHandle: valid.TaskHandle, ExternalKey: valid.ExternalKey, Response: "clear \x1b[2J now"},
		"oversized":         {OperationID: valid.OperationID, TaskHandle: valid.TaskHandle, ExternalKey: valid.ExternalKey, Response: strings.Repeat("a", 8193)},
	} {
		t.Run(name, func(t *testing.T) {
			refused := &mutationStore{}
			if _, err := respondMutations(t, refused).RespondDecision(context.Background(), command); err == nil {
				t.Fatalf("RespondDecision(%s) error = nil", name)
			}
			if refused.respondDecision.ExternalKey != "" {
				t.Fatalf("a refused answer reached the store: %#v", refused.respondDecision)
			}
		})
	}
}

// A commit failure surfaces as itself rather than as a success with no effect.
func TestMutations_RespondDecisionSurfacesACommitFailure(t *testing.T) {
	failure := errors.New("durable answer refused")
	store := &mutationStore{respondErr: failure}
	if _, err := respondMutations(t, store).RespondDecision(context.Background(), RespondDecisionCommand{
		OperationID: "operation-respond-0002", TaskHandle: "task-0001",
		ExternalKey: "schema-choice", Response: "use the versioned schema",
	}); !errors.Is(err, failure) {
		t.Fatalf("RespondDecision(commit failure) error = %v", err)
	}
}
