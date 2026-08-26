package reporter

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ContractArtifactReader is the task-bound content authority exposed by one
// protected runtime attachment. It accepts no task selector.
type ContractArtifactReader interface {
	ReadContractArtifact(context.Context, string) (domain.ContractArtifactContent, error)
}

func (server *RuntimeServer) readContractArtifact(ctx context.Context, request runtimeRequest) RuntimeOutcome {
	if request.Report != nil || request.Acknowledgement != nil || request.ExternalKey != "" ||
		domain.ValidateContractArtifactHandle(request.ArtifactHandle) != nil {
		return runtimeRejected("malformed_request")
	}
	if server.contractArtifacts == nil {
		return runtimeRejected("artifact_unavailable")
	}
	content, err := server.contractArtifacts.ReadContractArtifact(ctx, request.ArtifactHandle)
	if err != nil || content.Validate() != nil || content.Artifact.ArtifactHandle != request.ArtifactHandle {
		return runtimeRejected("artifact_unavailable")
	}
	content.Content = append([]byte(nil), content.Content...)
	return RuntimeOutcome{Version: runtimeProtocolVersion, ContractArtifact: &content}
}

// ReadContractArtifact fetches and verifies one exact artifact through the
// task-scoped attachment.
func (client *RuntimeClient) ReadContractArtifact(ctx context.Context, artifactHandle string) ([]byte, error) {
	if domain.ValidateContractArtifactHandle(artifactHandle) != nil {
		return nil, errors.New("read runtime contract artifact: handle is invalid")
	}
	outcome, err := client.call(ctx, runtimeRequest{
		Version: runtimeProtocolVersion, Kind: "artifact", ArtifactHandle: artifactHandle,
	})
	if err != nil {
		return nil, err
	}
	if outcome.Error != nil || outcome.ContractArtifact == nil || outcome.Brief != nil || outcome.Receipt != nil ||
		outcome.Acknowledgement != nil || outcome.AttentionResponse != nil ||
		outcome.ContractArtifact.Artifact.ArtifactHandle != artifactHandle || outcome.ContractArtifact.Validate() != nil {
		return nil, errors.New("read runtime contract artifact: attachment returned invalid content")
	}
	return append([]byte(nil), outcome.ContractArtifact.Content...), nil
}
