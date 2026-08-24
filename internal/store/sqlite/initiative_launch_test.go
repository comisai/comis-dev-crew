package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

func TestInitiativeLaunchAuthorizationBoundsActiveSchedulingFrontier(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	active := commitActiveInitiativeForTest(t, ctx, store, activation)
	unrelatedTask := storeTask("task-active-frontier-a", active.Initiative.StateVersion+1)
	unrelatedTask.State = domain.TaskReady
	unrelatedTask.ManagedRunID = "managed-run-active-frontier-a"
	unrelatedTask.WorkspaceLeaseID = "workspace-lease-active-frontier-a"
	if err := store.CreateTask(ctx, unrelatedTask); err != nil {
		t.Fatal(err)
	}
	unrelated := persistenceInitiative("initiative-active-frontier", domain.InitiativeActive, unrelatedTask.StateVersion)
	unrelated.Components[0].RepositoryID = unrelatedTask.RepositoryID
	unrelated.Components[0].TaskHandles = []string{unrelatedTask.Handle}
	unrelated.Components[1].RepositoryID = unrelatedTask.RepositoryID
	unrelated.Components[1].TaskHandles = []string{"task-active-frontier-integration"}
	unrelated.BaseRevisionSet[0].RepositoryID = unrelatedTask.RepositoryID
	unrelated.Edges[0].FromTaskHandle = unrelatedTask.Handle
	unrelated.Edges[0].ToTaskHandle = "task-active-frontier-integration"
	unrelated.IntegrationOwnerTask = "task-active-frontier-integration"
	unrelated.CreatedAt = active.Initiative.CreatedAt.Add(time.Hour)
	unrelated.UpdatedAt = unrelated.CreatedAt
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
	if err := authorizeInitiativeTaskStart(ctx, transaction, task, initiativeTestSchedulingLimits(1)); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(bounded active frontier) error = %v", err)
	}
}

func TestInitiativeLaunchAuthorizationSkipsEarlierCapacityIneligibleInitiatives(t *testing.T) {
	ctx := context.Background()
	store, _, activation := preparedInitiativeActivationStore(t)
	active := commitActiveInitiativeForTest(t, ctx, store, activation)
	occupied := storeTask("task-capped-repository-running", active.Initiative.StateVersion+1)
	occupied.State = domain.TaskWorking
	occupied.RepositoryID = "repo-capped"
	occupied.ManagedRunID = "managed-run-capped-repository"
	occupied.WorkspaceLeaseID = "workspace-lease-capped-repository"
	occupied, err := occupied.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(ctx, occupied); err != nil {
		t.Fatal(err)
	}
	olderTask := storeTask("task-capped-repository-ready", occupied.StateVersion+1)
	olderTask.State = domain.TaskReady
	olderTask.RepositoryID = occupied.RepositoryID
	olderTask.ManagedRunID = "managed-run-capped-ready"
	olderTask.WorkspaceLeaseID = "workspace-lease-capped-ready"
	olderTask, err = olderTask.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateTask(ctx, olderTask); err != nil {
		t.Fatal(err)
	}
	older := persistenceInitiative("initiative-capped-repository", domain.InitiativeActive, olderTask.StateVersion)
	older.BaseRevisionSet[0].RepositoryID = olderTask.RepositoryID
	older.Components = older.Components[:1]
	older.Components[0].RepositoryID = olderTask.RepositoryID
	older.Components[0].TaskHandles = []string{olderTask.Handle}
	older.Edges = nil
	older.ContractArtifacts = nil
	older.IntegrationOwnerTask = ""
	older.CreatedAt = active.Initiative.CreatedAt.Add(-time.Hour)
	older.UpdatedAt = older.CreatedAt
	if err := store.CreateInitiative(ctx, older); err != nil {
		t.Fatal(err)
	}
	target, err := store.GetTask(ctx, activation.Members[0].ExternalRunRef)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	limits := initiativeTestSchedulingLimits(2)
	limits.MaxConcurrentTasksPerRepository = 1

	if err := authorizeInitiativeTaskStart(ctx, transaction, target, limits); err != nil {
		t.Fatalf("authorizeInitiativeTaskStart(after capped initiative) error = %v", err)
	}
}
