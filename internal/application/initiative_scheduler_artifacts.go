package application

import (
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func indexSchedulingArtifacts(
	initiatives []domain.DevelopmentInitiative,
	artifacts []domain.ComponentContractArtifact,
) (map[string]map[string]domain.ComponentContractArtifact, error) {
	indexed := make(map[string]map[string]domain.ComponentContractArtifact, len(initiatives))
	listed := make(map[string]map[string]struct{}, len(initiatives))
	initiativeByHandle := make(map[string]domain.DevelopmentInitiative, len(initiatives))
	for _, initiative := range initiatives {
		if _, duplicate := listed[initiative.Handle]; duplicate {
			return nil, errors.New("schedule initiatives: initiative handles must be unique")
		}
		listed[initiative.Handle] = make(map[string]struct{}, len(initiative.ContractArtifacts))
		indexed[initiative.Handle] = make(map[string]domain.ComponentContractArtifact, len(initiative.ContractArtifacts))
		initiativeByHandle[initiative.Handle] = initiative
		for _, handle := range initiative.ContractArtifacts {
			listed[initiative.Handle][handle] = struct{}{}
		}
	}
	for _, artifact := range artifacts {
		if err := artifact.Validate(); err != nil {
			return nil, fmt.Errorf("schedule contract artifact %q: %w", artifact.ArtifactHandle, err)
		}
		initiativeArtifacts, found := indexed[artifact.InitiativeHandle]
		if !found {
			return nil, errors.New("schedule initiatives: contract artifact belongs to an unknown initiative")
		}
		if !initiativeByHandle[artifact.InitiativeHandle].ContainsTask(artifact.ProducerTaskHandle) {
			return nil, errors.New("schedule initiatives: contract artifact producer is outside the initiative")
		}
		if _, duplicate := initiativeArtifacts[artifact.ArtifactHandle]; duplicate {
			return nil, errors.New("schedule initiatives: contract artifact handles must be unique")
		}
		initiativeArtifacts[artifact.ArtifactHandle] = artifact
	}
	for initiativeHandle, handles := range listed {
		for handle := range handles {
			if _, found := indexed[initiativeHandle][handle]; !found {
				return nil, errors.New("schedule initiatives: current contract artifact registry is incomplete")
			}
		}
	}
	return indexed, nil
}
