package service

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func (*runtimeAttachmentRecoveryStore) ReadTaskContractArtifact(
	context.Context,
	string,
	string,
) (domain.ContractArtifactContent, error) {
	return domain.ContractArtifactContent{}, application.ErrNotFound
}

func (*runtimeTransitionStore) ReadTaskContractArtifact(
	context.Context,
	string,
	string,
) (domain.ContractArtifactContent, error) {
	return domain.ContractArtifactContent{}, application.ErrNotFound
}
