package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestReadTaskContractArtifactReturnsOnlyExactPinnedContent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutation := sqlitePreparedInitiativeMutation()
	content := []byte(`{"version":1}`)
	prepared := preparedContractArtifact(
		mutation.Initiative, "artifact-api-v1", "task-component-a",
		domain.ArtifactAPISchema, "application/json", content,
	)
	mutation.Initiative.ContractArtifacts = []string{prepared.Artifact.ArtifactHandle}
	consumer := mutation.Members[1].Task
	consumer.ConsumedContracts = []domain.PinnedContract{{
		ArtifactHandle: prepared.Artifact.ArtifactHandle,
		Kind:           prepared.Artifact.Kind,
		ContentHash:    prepared.Artifact.ContentHash,
	}}
	mutation.Members[1].Task, err = consumer.PinBriefRevision()
	if err != nil {
		t.Fatal(err)
	}
	mutation.ContractArtifacts = []application.PreparedInitiativeContractArtifact{prepared}
	recordInitiativeMemberIntents(t, store, mutation)
	if _, err := store.CommitPreparedInitiative(ctx, mutation); err != nil {
		t.Fatalf("CommitPreparedInitiative() error = %v", err)
	}
	//lint:ignore SA1012 The store boundary rejects nil before beginning a read transaction.
	if _, err := store.ReadTaskContractArtifact(nil, consumer.Handle, prepared.Artifact.ArtifactHandle); err == nil {
		t.Fatal("ReadTaskContractArtifact(nil context) error = nil")
	}
	for _, selector := range [][2]string{
		{"bad task", prepared.Artifact.ArtifactHandle},
		{consumer.Handle, "bad artifact"},
		{"task-contract-missing", prepared.Artifact.ArtifactHandle},
	} {
		if _, err := store.ReadTaskContractArtifact(ctx, selector[0], selector[1]); err == nil {
			t.Fatalf("ReadTaskContractArtifact(%q, %q) error = nil", selector[0], selector[1])
		}
	}

	got, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle)
	if err != nil || got.Artifact != prepared.Artifact || string(got.Content) != string(content) {
		t.Fatalf("ReadTaskContractArtifact() = %#v, %v", got, err)
	}
	got.Content[0] = 'x'
	reloaded, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle)
	if err != nil || string(reloaded.Content) != string(content) {
		t.Fatalf("ReadTaskContractArtifact(reload) = %#v, %v", reloaded, err)
	}
	unrelated := persistenceInitiative("initiative-unrelated-artifact-read", domain.InitiativeDelivered, 3)
	unrelated.Components[0].TaskHandles = []string{"task-unrelated-artifact-producer"}
	unrelated.Components[1].TaskHandles = []string{"task-unrelated-artifact-consumer"}
	unrelated.Edges[0].FromTaskHandle = "task-unrelated-artifact-producer"
	unrelated.Edges[0].ToTaskHandle = "task-unrelated-artifact-consumer"
	unrelated.IntegrationOwnerTask = "task-unrelated-artifact-consumer"
	if err := store.CreateInitiative(ctx, unrelated); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE initiatives SET components_json = '{' WHERE handle = ?", unrelated.Handle,
	); err != nil {
		t.Fatal(err)
	}
	if exact, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); err != nil || string(exact.Content) != string(content) {
		t.Fatalf("ReadTaskContractArtifact(unrelated corrupt history) = %#v, %v", exact, err)
	}
	if _, err := store.ReadTaskContractArtifact(
		ctx, mutation.Members[0].Task.Handle, prepared.Artifact.ArtifactHandle,
	); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadTaskContractArtifact(unpinned producer) error = %v, want ErrNotFound", err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, "artifact-unpinned-v1"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadTaskContractArtifact(unpinned handle) error = %v, want ErrNotFound", err)
	}

	overlap := mutation.Initiative
	overlap.Handle = "initiative-artifact-overlap"
	overlap.ManagedRunGroupID = ""
	if err := store.CreateInitiative(ctx, overlap); err != nil {
		t.Fatalf("CreateInitiative(overlap) error = %v", err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); err == nil {
		t.Fatal("ReadTaskContractArtifact(overlapping initiatives) error = nil")
	}
	if _, err := store.db.ExecContext(ctx, `DELETE FROM initiatives WHERE handle = ?`, overlap.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE initiatives SET contract_artifacts_json = '[]' WHERE handle = ?`, mutation.Initiative.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); err == nil {
		t.Fatal("ReadTaskContractArtifact(missing inventory) error = nil")
	}
	if _, err := store.db.ExecContext(ctx, `UPDATE initiatives SET contract_artifacts_json = ? WHERE handle = ?`,
		`["`+prepared.Artifact.ArtifactHandle+`"]`, mutation.Initiative.Handle); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE initiative_contract_artifacts SET kind = ? WHERE initiative_handle = ? AND artifact_handle = ?`,
		domain.ArtifactGeneratedClient, mutation.Initiative.Handle, prepared.Artifact.ArtifactHandle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); err == nil {
		t.Fatal("ReadTaskContractArtifact(mismatched pin metadata) error = nil")
	}
	if _, err := store.db.ExecContext(ctx,
		`UPDATE initiative_contract_artifacts SET kind = ? WHERE initiative_handle = ? AND artifact_handle = ?`,
		domain.ArtifactAPISchema, mutation.Initiative.Handle, prepared.Artifact.ArtifactHandle,
	); err != nil {
		t.Fatal(err)
	}

	if _, err := store.db.ExecContext(ctx,
		`UPDATE initiative_contract_artifacts SET content = ? WHERE initiative_handle = ? AND artifact_handle = ?`,
		[]byte(`{"version":2}`), mutation.Initiative.Handle, prepared.Artifact.ArtifactHandle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); err == nil {
		t.Fatal("ReadTaskContractArtifact(corrupt bytes) error = nil")
	}
	if _, err := store.db.ExecContext(ctx,
		`DELETE FROM initiative_contract_artifacts WHERE initiative_handle = ? AND artifact_handle = ?`,
		mutation.Initiative.Handle, prepared.Artifact.ArtifactHandle,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadTaskContractArtifact(missing content row) error = %v, want ErrNotFound", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle); err == nil {
		t.Fatal("ReadTaskContractArtifact(closed store) error = nil")
	}
}
