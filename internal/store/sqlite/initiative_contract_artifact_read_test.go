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

	got, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle)
	if err != nil || got.Artifact != prepared.Artifact || string(got.Content) != string(content) {
		t.Fatalf("ReadTaskContractArtifact() = %#v, %v", got, err)
	}
	got.Content[0] = 'x'
	reloaded, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, prepared.Artifact.ArtifactHandle)
	if err != nil || string(reloaded.Content) != string(content) {
		t.Fatalf("ReadTaskContractArtifact(reload) = %#v, %v", reloaded, err)
	}
	if _, err := store.ReadTaskContractArtifact(
		ctx, mutation.Members[0].Task.Handle, prepared.Artifact.ArtifactHandle,
	); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadTaskContractArtifact(unpinned producer) error = %v, want ErrNotFound", err)
	}
	if _, err := store.ReadTaskContractArtifact(ctx, consumer.Handle, "artifact-unpinned-v1"); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("ReadTaskContractArtifact(unpinned handle) error = %v, want ErrNotFound", err)
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
}
