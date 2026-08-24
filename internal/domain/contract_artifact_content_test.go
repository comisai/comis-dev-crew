package domain_test

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestContractArtifactContentRequiresExactBoundedBytes(t *testing.T) {
	bytes := []byte(`{"version":1}`)
	content := domain.ContractArtifactContent{
		Artifact: domain.ComponentContractArtifact{
			ArtifactHandle: "artifact-api-v1", InitiativeHandle: "initiative-contract-v1",
			ProducerTaskHandle: "task-contract-v1", Kind: domain.ArtifactAPISchema,
			ContentHash: fmt.Sprintf("%x", sha256.Sum256(bytes)), SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			MediaType: "application/json", Size: int64(len(bytes)),
			ProducedAt: time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC),
		},
		Content: bytes,
	}
	if err := content.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	content.Content = []byte(`{"version":2}`)
	if err := content.Validate(); err == nil {
		t.Fatal("Validate(altered bytes) error = nil")
	}
	content.Content = nil
	if err := content.Validate(); err == nil {
		t.Fatal("Validate(missing bytes) error = nil")
	}
}
