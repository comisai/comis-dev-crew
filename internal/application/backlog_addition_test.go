package application

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestBacklogAdditionCreatesOneBoundedRecordAndReplaysBeforeMintingIdentity(t *testing.T) {
	store := &backlogAdditionStoreStub{}
	identityCalls := 0
	additions, err := NewBacklogAdditions(BacklogAdditionConfig{
		Store: store,
		BacklogIDs: func(string) (string, error) {
			identityCalls++
			return "backlog-added-0001", nil
		},
		Clock: backlogAdditionClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := BacklogAdditionCommand{
		OperationID: "operation-add-backlog", RepositoryID: "repo-primary",
		Shape: domain.ShapeShip, RequestedOutcome: "Implement the bounded request.",
		DependsOn: []string{"backlog-existing"}, Priority: domain.BacklogPriorityHigh,
		Readiness:             domain.BacklogReady,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
	}
	result, err := additions.AddBacklog(context.Background(), command)
	if err != nil {
		t.Fatalf("AddBacklog() error = %v", err)
	}
	if result.Item.Handle != "backlog-added-0001" || result.Item.SchemaVersion != 1 ||
		result.Item.RepositoryID != command.RepositoryID || result.Item.Shape != command.Shape ||
		result.Item.Readiness != domain.BacklogReady || result.Operation.ID != command.OperationID ||
		!result.Item.CreatedAt.Equal(backlogAdditionClock()) || !result.Item.UpdatedAt.Equal(backlogAdditionClock()) {
		t.Fatalf("AddBacklog() = %#v", result)
	}
	for _, field := range domain.BacklogItemFieldNames(result.Item) {
		switch field {
		case "ManagedRunID", "WorkspaceLeaseID", "ExecutionAttachmentID", "Credential", "DeliveryMode":
			t.Fatalf("backlog addition gained run authority field %q", field)
		}
	}
	if identityCalls != 1 || store.commitCalls != 1 {
		t.Fatalf("identity/commit calls = %d/%d", identityCalls, store.commitCalls)
	}

	store.replay = &result
	replayed, err := additions.AddBacklog(context.Background(), command)
	if err != nil || replayed.Item.Handle != result.Item.Handle || replayed.Operation.ID != result.Operation.ID {
		t.Fatalf("AddBacklog(replay) = %#v, %v", replayed, err)
	}
	if identityCalls != 1 || store.commitCalls != 1 {
		t.Fatalf("replay repeated identity/commit calls = %d/%d", identityCalls, store.commitCalls)
	}
}

func TestBacklogAdditionRejectsTerminalInitialReadinessAndInvalidComposition(t *testing.T) {
	if _, err := NewBacklogAdditions(BacklogAdditionConfig{}); err == nil {
		t.Fatal("NewBacklogAdditions(empty) error = nil")
	}
	store := &backlogAdditionStoreStub{}
	additions, err := NewBacklogAdditions(BacklogAdditionConfig{
		Store: store, BacklogIDs: func(string) (string, error) { return "backlog-added-0002", nil },
		Clock: backlogAdditionClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, readiness := range []domain.BacklogReadiness{domain.BacklogPromoted, domain.BacklogDropped} {
		_, err := additions.AddBacklog(context.Background(), BacklogAdditionCommand{
			OperationID: "operation-add-terminal", RepositoryID: "repo-primary",
			Shape: domain.ShapeShip, RequestedOutcome: "Implement the bounded request.",
			DependsOn: []string{}, Priority: domain.BacklogPriorityNormal, Readiness: readiness,
			SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		})
		if err == nil {
			t.Fatalf("AddBacklog(%q) error = nil", readiness)
		}
	}
	if store.commitCalls != 0 {
		t.Fatalf("invalid additions reached store %d times", store.commitCalls)
	}
}

type backlogAdditionStoreStub struct {
	replay      *BacklogAdditionResult
	commitCalls int
}

func (store *backlogAdditionStoreStub) ReplayBacklogAddition(
	context.Context,
	string,
	string,
) (BacklogAdditionResult, bool, error) {
	if store.replay == nil {
		return BacklogAdditionResult{}, false, nil
	}
	return *store.replay, true, nil
}

func (store *backlogAdditionStoreStub) CommitBacklogAddition(
	_ context.Context,
	mutation BacklogAdditionMutation,
) (BacklogAdditionResult, error) {
	store.commitCalls++
	return BacklogAdditionResult{
		Item: mutation.Item,
		Operation: completedBacklogOperation(
			mutation.OperationID, "AddBacklog", mutation.SubjectDigest, mutation.Item.Handle, mutation.At,
		),
	}, nil
}

func completedBacklogOperation(id, command, digest, resultRef string, at time.Time) domain.OperationRecord {
	return domain.OperationRecord{
		SchemaVersion: 1, ID: id, Command: command, SubjectDigest: digest,
		Status: domain.OperationCompleted, ResultRef: resultRef, StateVersion: 1,
		CreatedAt: at, UpdatedAt: at,
	}
}

func backlogAdditionClock() time.Time {
	return time.Date(2026, time.August, 20, 20, 0, 0, 0, time.UTC)
}
