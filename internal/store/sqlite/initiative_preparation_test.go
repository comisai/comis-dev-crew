package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestPreparedInitiativeCommitsAndReplaysAllMembersInOneTransaction(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	mutation := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, mutation)
	result, err := store.CommitPreparedInitiative(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	if len(result.Tasks) != 2 || len(result.Preparation.Members) != 2 ||
		result.Initiative.StateVersion != result.Operation.StateVersion {
		t.Fatalf("CommitPreparedInitiative() = %#v, want one versioned two-member result", result)
	}
	for _, task := range result.Tasks {
		if task.StateVersion != result.Operation.StateVersion {
			t.Fatalf("task %q version = %d, want %d", task.Handle, task.StateVersion, result.Operation.StateVersion)
		}
		operationID := initiativeMemberOperationForTest(mutation, task.Handle)
		operation, err := store.GetOperation(ctx, operationID)
		if err != nil || operation.Command != "PrepareTask" || operation.ResultRef != task.Handle ||
			operation.StateVersion != result.Operation.StateVersion {
			t.Fatalf("member operation %q = %#v, %v", operationID, operation, err)
		}
	}
	intents, err := store.ListTaskPreparationIntents(ctx)
	if err != nil || len(intents) != 0 {
		t.Fatalf("remaining member intents = %#v, %v, want none", intents, err)
	}
	replayed, found, err := store.ReplayInitiativePreparation(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(replayed, result) {
		t.Fatalf("ReplayInitiativePreparation() = %#v, %t, %v, want %#v", replayed, found, err, result)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted, found, err := reopened.ReplayInitiativePreparation(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(restarted, result) {
		t.Fatalf("ReplayInitiativePreparation(restart) = %#v, %t, %v, want %#v", restarted, found, err, result)
	}
	if _, _, err := reopened.ReplayInitiativePreparation(
		ctx, mutation.OperationID, strings.Repeat("f", 64),
	); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("ReplayInitiativePreparation(altered) error = %v, want ErrConflict", err)
	}
}

func TestPreparedInitiativeReplayPreservesOriginalProjectionAfterActivation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, mutation)
	prepared, err := store.CommitPreparedInitiative(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	if _, err := store.CommitInitiativeActivation(ctx, preparedInitiativeActivationMutation(mutation)); err != nil {
		t.Fatalf("CommitInitiativeActivation() error = %v", err)
	}

	replayed, found, err := store.ReplayInitiativePreparation(ctx, mutation.OperationID, mutation.SubjectDigest)
	if err != nil || !found || !reflect.DeepEqual(replayed, prepared) {
		t.Fatalf("ReplayInitiativePreparation(after activation) = %#v, %t, %v, want %#v", replayed, found, err, prepared)
	}
}

func TestPreparedInitiativeReplayDoesNotReopenAbandonedMemberAuthority(t *testing.T) {
	ctx := context.Background()
	store, abandonment := preparedInitiativeAbandonStore(t, application.AbandonDispositionReapSafe)
	prepared := sqlitePreparedInitiativeMutation()
	if _, err := store.CommitInitiativeAbandonment(ctx, abandonment); err != nil {
		t.Fatalf("CommitInitiativeAbandonment() error = %v", err)
	}

	replayed, found, err := store.ReplayInitiativePreparation(ctx, prepared.OperationID, prepared.SubjectDigest)
	if err != nil || !found {
		t.Fatalf("ReplayInitiativePreparation(after abandonment) = %#v, %t, %v", replayed, found, err)
	}
	for _, preparation := range replayed.Preparation.Members {
		if preparation.State != application.PreparationAbandoned ||
			preparation.AbandonReason != abandonment.Reason ||
			preparation.Disposition != abandonment.Disposition || preparation.ClosedAt == nil {
			t.Fatalf("replayed abandoned preparation = %#v, want closed authority", preparation)
		}
	}
}

func TestInitiativePreparationProjectionRejectsCorruptReplayRecords(t *testing.T) {
	prepared := sqlitePreparedInitiativeMutation()
	operation := completedMutationOperation(
		prepared.OperationID, commandPrepareInitiative, prepared.SubjectDigest,
		prepared.Initiative.Handle, 1, prepared.At,
	)
	tests := []struct {
		name      string
		operation domain.OperationRecord
		tasks     []domain.Task
	}{
		{
			name: "wrong operation command",
			operation: func() domain.OperationRecord {
				corrupt := operation
				corrupt.Command = commandPrepareTask
				return corrupt
			}(),
			tasks: []domain.Task{prepared.Members[0].Task},
		},
		{
			name:      "member creation differs from operation",
			operation: operation,
			tasks: func() []domain.Task {
				corrupt := prepared.Members[0].Task
				corrupt.CreatedAt = corrupt.CreatedAt.Add(-time.Second)
				return []domain.Task{corrupt}
			}(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, _, err := restoreInitiativePreparationProjection(
				prepared.Initiative, test.tasks, test.operation,
			); err == nil {
				t.Fatal("restoreInitiativePreparationProjection(corrupt record) error = nil")
			}
		})
	}
}

func TestPreparedInitiativeCommitsFiveMemberFullStackGraph(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	handles := []string{
		"task-contract", "task-backend", "task-frontend", "task-integration", "task-validation",
	}
	mutation.Initiative.Components = make([]domain.InitiativeComponent, 0, len(handles))
	mutation.Initiative.ContractArtifacts = []string{"artifact-api-v1"}
	contractArtifact := preparedContractArtifact(
		mutation.Initiative, "artifact-api-v1", "task-contract", domain.ArtifactAPISchema,
		"application/json", []byte(`{"version":1}`),
	)
	mutation.ContractArtifacts = []application.PreparedInitiativeContractArtifact{contractArtifact}
	mutation.Members = make([]application.PreparedInitiativeMember, 0, len(handles))
	for index, handle := range handles {
		mutation.Initiative.Components = append(mutation.Initiative.Components, domain.InitiativeComponent{
			ComponentHandle: "component-" + handle,
			RepositoryID:    "repo-primary", ResponsibilityRef: "responsibility-" + handle,
			TaskHandles: []string{handle},
		})
		task := storeTask(handle, 1)
		task.RepositoryID = "repo-primary"
		task.BaseRevision = mutation.Initiative.BaseRevisionSet[0].Revision
		if handle == "task-backend" || handle == "task-frontend" {
			task.ConsumedContracts = []domain.PinnedContract{{
				ArtifactHandle: "artifact-api-v1", Kind: domain.ArtifactAPISchema,
				ContentHash: contractArtifact.Artifact.ContentHash,
			}}
		}
		task.CreatedAt = mutation.At
		task.UpdatedAt = mutation.At
		task, err = task.PinBriefRevision()
		if err != nil {
			t.Fatalf("PinBriefRevision(%q) error = %v", handle, err)
		}
		mutation.Members = append(mutation.Members, application.PreparedInitiativeMember{
			Task: task,
			Preparation: application.ManagedRunPreparation{
				ExternalRunRef: handle, RegistrationNonce: fmt.Sprintf("registration-nonce_member_%d", index),
				RequestedWorkspaceRoot: "/approved/workspaces/" + handle,
				RequestedAttachment: application.PreparedRuntimeAttachment{
					Kind:          application.RuntimeAttachmentUnixSocket,
					SourcePath:    "/approved/runtime/" + handle + "/attachment.sock",
					RelayIdentity: strings.Repeat(fmt.Sprintf("%x", index+1), 64)[:64],
				},
				ExpiresAt: mutation.GroupExpiresAt, State: application.PreparationOpen,
			},
			OperationID:   fmt.Sprintf("prepare-member-full-stack-%d", index),
			SubjectDigest: strings.Repeat(fmt.Sprintf("%x", index+1), 64)[:64],
		})
	}
	mutation.Initiative.Edges = []domain.InitiativeEdge{
		{FromTaskHandle: "task-contract", ToTaskHandle: "task-backend", Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema},
		{FromTaskHandle: "task-contract", ToTaskHandle: "task-frontend", Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema},
		{FromTaskHandle: "task-backend", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
		{FromTaskHandle: "task-frontend", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
		{FromTaskHandle: "task-integration", ToTaskHandle: "task-validation", Kind: domain.EdgeBlocksStart},
	}
	mutation.Initiative.IntegrationOwnerTask = "task-integration"
	recordInitiativeMemberIntents(t, store, mutation)

	result, err := store.CommitPreparedInitiative(ctx, mutation)
	if err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	if len(result.Tasks) != len(handles) || len(result.Preparation.Members) != len(handles) {
		t.Fatalf("CommitPreparedInitiative() members = %d/%d, want %d", len(result.Tasks), len(result.Preparation.Members), len(handles))
	}
	if len(result.ContractArtifacts) != 1 ||
		result.ContractArtifacts[0].ProducerTaskHandle != "task-contract" ||
		result.ContractArtifacts[0].ContentHash != contractArtifact.Artifact.ContentHash {
		t.Fatalf("CommitPreparedInitiative() contract artifacts = %#v", result.ContractArtifacts)
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE initiative_contract_artifacts SET content = ? WHERE initiative_handle = ?`,
		[]byte(`{"version":2}`), mutation.Initiative.Handle,
	); err != nil {
		t.Fatalf("corrupt contract artifact content: %v", err)
	}
	if _, _, err := store.ReplayInitiativePreparation(
		ctx, mutation.OperationID, mutation.SubjectDigest,
	); err == nil {
		t.Fatal("ReplayInitiativePreparation(corrupt artifact bytes) error = nil")
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE initiative_contract_artifacts SET size = 'invalid' WHERE initiative_handle = ?`,
		mutation.Initiative.Handle,
	); err != nil {
		t.Fatalf("corrupt contract artifact size: %v", err)
	}
	if _, _, err := store.ReplayInitiativePreparation(
		ctx, mutation.OperationID, mutation.SubjectDigest,
	); err == nil {
		t.Fatal("ReplayInitiativePreparation(unscannable artifact) error = nil")
	}
}

func preparedContractArtifact(
	initiative domain.DevelopmentInitiative,
	artifactHandle string,
	producerTaskHandle string,
	kind domain.ContractArtifactKind,
	mediaType string,
	content []byte,
) application.PreparedInitiativeContractArtifact {
	digest := fmt.Sprintf("%x", sha256.Sum256(content))
	return application.PreparedInitiativeContractArtifact{
		Artifact: domain.ComponentContractArtifact{
			ArtifactHandle: artifactHandle, InitiativeHandle: initiative.Handle,
			ProducerTaskHandle: producerTaskHandle, Kind: kind, ContentHash: digest,
			SourceRevision: initiative.BaseRevisionSet[0].Revision,
			MediaType:      mediaType, Size: int64(len(content)), ProducedAt: initiative.CreatedAt,
		},
		Content: append([]byte(nil), content...),
	}
}

func TestPreparedInitiativeRollsBackEveryDurableRecordOnMemberFailure(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.db.ExecContext(ctx, `CREATE TRIGGER refuse_integration_member
		BEFORE INSERT ON tasks WHEN NEW.handle = 'task-integration'
		BEGIN SELECT RAISE(ABORT, 'injected member failure'); END`); err != nil {
		t.Fatalf("install member failure trigger: %v", err)
	}
	if _, err := store.CommitPreparedInitiative(ctx, mutation); err == nil {
		t.Fatal("CommitPreparedInitiative(injected failure) error = nil")
	}
	for table, want := range map[string]int{
		"initiatives": 0, "initiative_preparations": 0, "tasks": 0,
		"initiative_contract_artifacts": 0, "task_preparations": 0,
		"operations": 0, "task_preparation_intents": 2,
	} {
		var count int
		if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil { // #nosec G202 -- table names are a closed test fixture.
			t.Fatalf("count %s: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s rows = %d, want %d after rollback", table, count, want)
		}
	}
}

func TestPreparedInitiativeRejectsArtifactFromOutsideItsMemberSet(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	mutation.Initiative.ContractArtifacts = []string{"artifact-api-v1"}
	mutation.ContractArtifacts = []application.PreparedInitiativeContractArtifact{preparedContractArtifact(
		mutation.Initiative, "artifact-api-v1", "task-outside", domain.ArtifactAPISchema,
		"application/json", []byte(`{"version":1}`),
	)}
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.CommitPreparedInitiative(ctx, mutation); err == nil {
		t.Fatal("CommitPreparedInitiative(outside artifact producer) error = nil")
	}
	var initiatives int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM initiatives").Scan(&initiatives); err != nil {
		t.Fatal(err)
	}
	if initiatives != 0 {
		t.Fatalf("initiative rows = %d, want none", initiatives)
	}
}

func TestPreparedInitiativeRejectsInexactContractArtifactSets(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *application.PreparedInitiativeMutation)
	}{
		{name: "unlisted artifact", mutate: func(_ *testing.T, mutation *application.PreparedInitiativeMutation) {
			mutation.Initiative.ContractArtifacts = nil
		}},
		{name: "duplicate artifact", mutate: func(_ *testing.T, mutation *application.PreparedInitiativeMutation) {
			mutation.ContractArtifacts = append(mutation.ContractArtifacts, mutation.ContractArtifacts[0])
		}},
		{name: "incomplete artifact set", mutate: func(_ *testing.T, mutation *application.PreparedInitiativeMutation) {
			mutation.ContractArtifacts = nil
		}},
		{name: "mismatched consumer pin", mutate: func(t *testing.T, mutation *application.PreparedInitiativeMutation) {
			member := mutation.Members[1].Task
			member.ConsumedContracts = []domain.PinnedContract{{
				ArtifactHandle: "artifact-api-v1", Kind: domain.ArtifactAPISchema,
				ContentHash: strings.Repeat("f", 64),
			}}
			var err error
			mutation.Members[1].Task, err = member.PinBriefRevision()
			if err != nil {
				t.Fatalf("PinBriefRevision() error = %v", err)
			}
		}},
		{name: "unresolved producer edge", mutate: func(_ *testing.T, mutation *application.PreparedInitiativeMutation) {
			mutation.Initiative.Edges = []domain.InitiativeEdge{{
				FromTaskHandle: "task-component-a", ToTaskHandle: "task-integration",
				Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema,
			}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			mutation := sqlitePreparedInitiativeMutation()
			mutation.Initiative.ContractArtifacts = []string{"artifact-api-v1"}
			mutation.ContractArtifacts = []application.PreparedInitiativeContractArtifact{
				preparedContractArtifact(
					mutation.Initiative, "artifact-api-v1", "task-component-a",
					domain.ArtifactAPISchema, "application/json", []byte(`{"version":1}`),
				),
			}
			test.mutate(t, &mutation)
			recordInitiativeMemberIntents(t, store, mutation)
			if _, err := store.CommitPreparedInitiative(ctx, mutation); err == nil {
				t.Fatal("CommitPreparedInitiative(inexact artifact set) error = nil")
			}
			var initiatives int
			if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM initiatives").Scan(&initiatives); err != nil {
				t.Fatal(err)
			}
			if initiatives != 0 {
				t.Fatalf("initiative rows = %d, want none", initiatives)
			}
		})
	}
}

func TestPreparedInitiativeRejectsCrossServiceMemberAuthority(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	mutation.Members[1].Task.ServiceInstanceID = "foreign-service-instance"
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.CommitPreparedInitiative(ctx, mutation); err == nil {
		t.Fatal("CommitPreparedInitiative(cross-service member) error = nil")
	}
	var initiatives int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM initiatives").Scan(&initiatives); err != nil {
		t.Fatalf("count initiatives: %v", err)
	}
	if initiatives != 0 {
		t.Fatalf("initiative rows = %d, want none for cross-service authority", initiatives)
	}
}

func sqlitePreparedInitiativeMutation() application.PreparedInitiativeMutation {
	at := time.Date(2026, time.August, 20, 16, 0, 0, 0, time.UTC)
	initiative := persistenceInitiative("initiative-prepare-0001", domain.InitiativePreparing, 1)
	initiative.ManagedRunGroupID = ""
	initiative.ContractArtifacts = []string{}
	initiative.CreatedAt = at
	initiative.UpdatedAt = at
	members := make([]application.PreparedInitiativeMember, 0, 2)
	for index, handle := range []string{"task-component-a", "task-integration"} {
		task := storeTask(handle, 1)
		task.RepositoryID = "repo-primary"
		task.BaseRevision = initiative.BaseRevisionSet[0].Revision
		task.CreatedAt = at
		task.UpdatedAt = at
		task, _ = task.PinBriefRevision()
		operationID := "prepare-member-000" + string(rune('1'+index))
		members = append(members, application.PreparedInitiativeMember{
			Task: task,
			Preparation: application.ManagedRunPreparation{
				ExternalRunRef: handle, RegistrationNonce: "registration-nonce_" + handle,
				RequestedWorkspaceRoot: "/approved/workspaces/" + handle,
				RequestedAttachment: application.PreparedRuntimeAttachment{
					Kind:          application.RuntimeAttachmentUnixSocket,
					SourcePath:    "/approved/runtime/" + handle + "/attachment.sock",
					RelayIdentity: strings.Repeat("ab", 32),
				},
				ExpiresAt: at.Add(time.Hour), State: application.PreparationOpen,
			},
			OperationID: operationID, SubjectDigest: strings.Repeat(string(rune('a'+index)), 64),
		})
	}
	return application.PreparedInitiativeMutation{
		Initiative: initiative, Members: members,
		GroupRegistrationNonce: "registration-nonce_group", GroupExpiresAt: at.Add(time.Hour),
		OperationID: "prepare-initiative-store", SubjectDigest: strings.Repeat("c", 64), At: at,
	}
}

func recordInitiativeMemberIntents(
	t *testing.T,
	store *Store,
	mutation application.PreparedInitiativeMutation,
) {
	t.Helper()
	for _, member := range mutation.Members {
		if _, err := store.RecordTaskPreparationIntent(context.Background(), application.TaskPreparationIntent{
			OperationID: member.OperationID, TaskHandle: member.Task.Handle,
			SubjectDigest: member.SubjectDigest, CreatedAt: mutation.At,
		}); err != nil {
			t.Fatalf("RecordTaskPreparationIntent(%q) error = %v", member.Task.Handle, err)
		}
	}
}

func initiativeMemberOperationForTest(
	mutation application.PreparedInitiativeMutation,
	taskHandle string,
) string {
	for _, member := range mutation.Members {
		if member.Task.Handle == taskHandle {
			return member.OperationID
		}
	}
	return ""
}
