package reporter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestRuntimeArtifactClientRejectsAlteredOrMixedContent(t *testing.T) {
	bytes := []byte("schema: component.contract.v1\n")
	valid := domain.ContractArtifactContent{
		Artifact: domain.ComponentContractArtifact{
			ArtifactHandle: "artifact-boundary-v1", InitiativeHandle: "initiative-boundary-v1",
			ProducerTaskHandle: "task-boundary-v1", Kind: domain.ArtifactAPISchema,
			ContentHash:    fmt.Sprintf("%x", sha256.Sum256(bytes)),
			SourceRevision: strings.Repeat("a", 40), MediaType: "text/plain",
			Size: int64(len(bytes)), ProducedAt: time.Date(2026, time.August, 24, 10, 0, 0, 0, time.UTC),
		},
		Content: bytes,
	}
	altered := valid
	altered.Content = []byte("schema: component.contract.v2\n")
	wrongHandle := valid
	wrongHandle.Artifact.ArtifactHandle = "artifact-other-v1"
	brief := boundaryBrief("task-boundary-v1")
	for _, outcome := range []RuntimeOutcome{
		runtimeRejected("artifact_unavailable"),
		{Version: runtimeProtocolVersion, ContractArtifact: &altered},
		{Version: runtimeProtocolVersion, ContractArtifact: &wrongHandle},
		{Version: runtimeProtocolVersion, ContractArtifact: &valid, Brief: &brief},
	} {
		encoded, err := json.Marshal(outcome)
		if err != nil {
			t.Fatal(err)
		}
		client, done := startBoundaryResponder(t, append(encoded, '\n'))
		if _, err := client.ReadContractArtifact(context.Background(), valid.Artifact.ArtifactHandle); err == nil {
			t.Fatalf("ReadContractArtifact accepted outcome %#v", outcome)
		}
		done()
	}
}

func TestRuntimeArtifactServerRejectsUnverifiedReaderResults(t *testing.T) {
	request := runtimeRequest{
		Version: runtimeProtocolVersion, Kind: "artifact", ArtifactHandle: "artifact-boundary-v1",
	}
	server := &RuntimeServer{}
	if outcome := server.readContractArtifact(context.Background(), request); outcome.Error == nil {
		t.Fatal("readContractArtifact accepted a missing reader")
	}
	server.contractArtifacts = boundaryContractArtifactReader{}
	if outcome := server.readContractArtifact(context.Background(), request); outcome.Error == nil {
		t.Fatal("readContractArtifact accepted invalid reader content")
	}
	request.ExternalKey = "unexpected"
	if outcome := server.readContractArtifact(context.Background(), request); outcome.Error == nil ||
		outcome.Error.Code != "malformed_request" {
		t.Fatalf("readContractArtifact(mixed request) = %#v", outcome)
	}
}

type boundaryContractArtifactReader struct{}

func (boundaryContractArtifactReader) ReadContractArtifact(
	context.Context,
	string,
) (domain.ContractArtifactContent, error) {
	return domain.ContractArtifactContent{}, nil
}
