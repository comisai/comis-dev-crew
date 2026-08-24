package service

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type taskContractArtifactReader struct {
	store      runtimeAttachmentStore
	taskHandle string
}

func (reader taskContractArtifactReader) ReadContractArtifact(
	ctx context.Context,
	artifactHandle string,
) (domain.ContractArtifactContent, error) {
	return reader.store.ReadTaskContractArtifact(ctx, reader.taskHandle, artifactHandle)
}
