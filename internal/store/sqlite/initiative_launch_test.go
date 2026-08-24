package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeLaunchAuthorizationDoesNotClaimStandaloneTasks(t *testing.T) {
	store, err := Open(context.Background(), filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	task := storeTask("task-standalone-launch", 1)
	task.State = domain.TaskReady
	task.ManagedRunID = "managed-run-standalone-launch"
	task.WorkspaceLeaseID = "workspace-lease-standalone-launch"
	if err := store.CreateTask(context.Background(), task); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := authorizeInitiativeTaskStart(context.Background(), transaction, task, nil); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(standalone) error = %v", err)
	}
}

func TestInitiativeLaunchAuthorizationDoesNotReadHistoricalArtifactBodies(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	active := commitActiveInitiativeForTest(t, ctx, store, activation)
	historical := preparedContractArtifact(
		active.Initiative, "artifact-historical-api", activation.Members[0].ExternalRunRef,
		domain.ArtifactAPISchema, "application/json", []byte(`{"version":1}`),
	)
	if err := insertInitiativeContractArtifact(ctx, store.db, historical); err != nil {
		t.Fatalf("insert historical contract artifact: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE initiative_contract_artifacts
		SET content = X'00' WHERE initiative_handle = ? AND artifact_handle = ?`,
		active.Initiative.Handle, historical.Artifact.ArtifactHandle,
	); err != nil {
		t.Fatalf("corrupt irrelevant historical artifact body: %v", err)
	}
	task, err := store.GetTask(ctx, activation.Members[0].ExternalRunRef)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := authorizeInitiativeTaskStart(ctx, transaction, task, initiativeTestSchedulingLimits(2)); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(with corrupt historical body) error = %v", err)
	}
}

func TestInitiativeLaunchAuthorizationStillRejectsInvalidArtifactMetadata(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	active := commitActiveInitiativeForTest(t, ctx, store, activation)
	historical := preparedContractArtifact(
		active.Initiative, "artifact-invalid-metadata", activation.Members[0].ExternalRunRef,
		domain.ArtifactAPISchema, "application/json", []byte(`{"version":1}`),
	)
	if err := insertInitiativeContractArtifact(ctx, store.db, historical); err != nil {
		t.Fatalf("insert historical contract artifact: %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE initiative_contract_artifacts
		SET content_hash = 'invalid' WHERE initiative_handle = ? AND artifact_handle = ?`,
		active.Initiative.Handle, historical.Artifact.ArtifactHandle,
	); err != nil {
		t.Fatalf("corrupt artifact metadata: %v", err)
	}
	task, err := store.GetTask(ctx, activation.Members[0].ExternalRunRef)
	if err != nil {
		t.Fatalf("GetTask() error = %v", err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx() error = %v", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := authorizeInitiativeTaskStart(ctx, transaction, task, initiativeTestSchedulingLimits(2)); err == nil {
		t.Fatal("authorizeInitiativeTaskStart(with invalid artifact metadata) error = nil")
	}
}

func TestInitiativeLaunchAuthorizationDoesNotMaterializeTerminalHistory(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	commitActiveInitiativeForTest(t, ctx, store, activation)
	unrelated := persistenceInitiative("initiative-terminal-launch-history", domain.InitiativeDelivered, 3)
	unrelated.Components[0].TaskHandles = []string{"task-terminal-launch-a"}
	unrelated.Components[1].TaskHandles = []string{"task-terminal-launch-b"}
	unrelated.Edges[0].FromTaskHandle = "task-terminal-launch-a"
	unrelated.Edges[0].ToTaskHandle = "task-terminal-launch-b"
	unrelated.IntegrationOwnerTask = "task-terminal-launch-b"
	if err := store.CreateInitiative(ctx, unrelated); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE initiatives SET components_json = '{' WHERE handle = ?", unrelated.Handle,
	); err != nil {
		t.Fatal(err)
	}
	task, err := store.GetTask(ctx, activation.Members[0].ExternalRunRef)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	if err := authorizeInitiativeTaskStart(ctx, transaction, task, initiativeTestSchedulingLimits(2)); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(unrelated terminal history) error = %v", err)
	}
}
