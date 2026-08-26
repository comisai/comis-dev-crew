package application

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func buildInitiativeContractArtifacts(
	command PrepareInitiativeCommand,
	initiativeHandle string,
	drafts []initiativeMemberDraft,
	refs map[string]string,
	at time.Time,
) ([]PreparedInitiativeContractArtifact, error) {
	tasksByHandle := make(map[string]domain.Task, len(drafts))
	for _, draft := range drafts {
		tasksByHandle[draft.task.Handle] = draft.task
	}
	artifacts := make([]PreparedInitiativeContractArtifact, 0, len(command.ContractArtifacts))
	seenHandles := make(map[string]struct{}, len(command.ContractArtifacts))
	type producerKind struct {
		producer string
		kind     domain.ContractArtifactKind
	}
	seenProducerKinds := make(map[producerKind]struct{}, len(command.ContractArtifacts))
	for _, input := range command.ContractArtifacts {
		producerHandle, found := refs[input.ProducerTaskRef]
		if !found {
			return nil, errors.New("contract artifact producer is outside the initiative")
		}
		content := []byte(input.Content)
		digest := fmt.Sprintf("%x", sha256.Sum256(content))
		artifact := domain.ComponentContractArtifact{
			ArtifactHandle: input.ArtifactHandle, InitiativeHandle: initiativeHandle,
			ProducerTaskHandle: producerHandle, Kind: input.Kind, ContentHash: digest,
			SourceRevision: tasksByHandle[producerHandle].BaseRevision,
			MediaType:      input.MediaType, Size: int64(len(content)), ProducedAt: at,
		}
		if err := artifact.Validate(); err != nil {
			return nil, err
		}
		if _, duplicate := seenHandles[artifact.ArtifactHandle]; duplicate {
			return nil, errors.New("contract artifact handles must be unique")
		}
		key := producerKind{producer: artifact.ProducerTaskHandle, kind: artifact.Kind}
		if _, duplicate := seenProducerKinds[key]; duplicate {
			return nil, errors.New("contract artifact producer and kind must be unique")
		}
		seenHandles[artifact.ArtifactHandle] = struct{}{}
		seenProducerKinds[key] = struct{}{}
		artifacts = append(artifacts, PreparedInitiativeContractArtifact{Artifact: artifact, Content: content})
	}
	return artifacts, nil
}

func validateInitiativeContractPins(
	initiative domain.DevelopmentInitiative,
	drafts []initiativeMemberDraft,
	artifacts []PreparedInitiativeContractArtifact,
) error {
	byHandle := make(map[string]domain.ComponentContractArtifact, len(artifacts))
	for _, artifact := range artifacts {
		byHandle[artifact.Artifact.ArtifactHandle] = artifact.Artifact
	}
	tasks := make(map[string]domain.Task, len(drafts))
	for _, draft := range drafts {
		tasks[draft.task.Handle] = draft.task
		for _, pin := range draft.task.ConsumedContracts {
			artifact, found := byHandle[pin.ArtifactHandle]
			if !found || artifact.Kind != pin.Kind || artifact.ContentHash != pin.ContentHash {
				return errors.New("consumed contract does not resolve to an exact initiative artifact")
			}
		}
	}
	for _, edge := range initiative.Edges {
		if edge.Kind != domain.EdgeConsumesArtifact {
			continue
		}
		resolved := false
		for _, pin := range tasks[edge.ToTaskHandle].ConsumedContracts {
			artifact, found := byHandle[pin.ArtifactHandle]
			if found && pin.Kind == edge.RequiredArtifactKind &&
				artifact.Kind == edge.RequiredArtifactKind &&
				artifact.ProducerTaskHandle == edge.FromTaskHandle &&
				artifact.ContentHash == pin.ContentHash {
				resolved = true
				break
			}
		}
		if !resolved {
			return errors.New("artifact dependency does not resolve to its recorded producer")
		}
	}
	return nil
}
