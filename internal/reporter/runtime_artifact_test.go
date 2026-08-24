package reporter_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestRuntimeAttachmentReadsOnlyVerifiedTaskScopedContractArtifact(t *testing.T) {
	harness := newRuntimeHarness(t, "task-runtime-artifact", "report-runtime-artifact")
	content, err := harness.client.ReadContractArtifact(context.Background(), "artifact-runtime-v1")
	calls, handle := harness.artifacts.observed()
	if err != nil || string(content) != string(harness.artifacts.content.Content) ||
		calls != 1 || handle != "artifact-runtime-v1" {
		t.Fatalf("ReadContractArtifact() = %q, %v; reader=%#v", content, err, harness.artifacts)
	}
	content[0] = 'x'
	if string(harness.artifacts.content.Content) == string(content) {
		t.Fatal("ReadContractArtifact returned aliased content")
	}
	if _, err := harness.client.ReadContractArtifact(context.Background(), "bad handle"); err == nil {
		t.Fatal("ReadContractArtifact(malformed) error = nil")
	}
	if calls, _ := harness.artifacts.observed(); calls != 1 {
		t.Fatalf("malformed artifact reached reader; calls=%d", calls)
	}
	if _, err := harness.client.ReadContractArtifact(context.Background(), "artifact-unpinned-v1"); err == nil {
		t.Fatal("ReadContractArtifact(unpinned) error = nil")
	}
	if calls, _ := harness.artifacts.observed(); calls != 2 {
		t.Fatalf("unpinned artifact reader calls=%d", calls)
	}
}

type recordingContractArtifactReader struct {
	mu      sync.Mutex
	content domain.ContractArtifactContent
	calls   int
	handle  string
}

func newRecordingContractArtifactReader(taskHandle string) *recordingContractArtifactReader {
	bytes := []byte("schema: component.contract.v1\n")
	return &recordingContractArtifactReader{content: domain.ContractArtifactContent{
		Artifact: domain.ComponentContractArtifact{
			ArtifactHandle: "artifact-runtime-v1", InitiativeHandle: "initiative-runtime-v1",
			ProducerTaskHandle: taskHandle, Kind: domain.ArtifactAPISchema,
			ContentHash:    fmt.Sprintf("%x", sha256.Sum256(bytes)),
			SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			MediaType:      "text/plain", Size: int64(len(bytes)),
			ProducedAt: time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC),
		},
		Content: bytes,
	}}
}

func (reader *recordingContractArtifactReader) ReadContractArtifact(
	_ context.Context,
	handle string,
) (domain.ContractArtifactContent, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	reader.calls++
	reader.handle = handle
	if handle != reader.content.Artifact.ArtifactHandle {
		return domain.ContractArtifactContent{}, errors.New("private unpinned artifact")
	}
	result := reader.content
	result.Content = append([]byte(nil), result.Content...)
	return result, nil
}

func (reader *recordingContractArtifactReader) observed() (int, string) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.calls, reader.handle
}
