package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const initiativeContractArtifactMigration = `
CREATE TABLE initiative_contract_artifacts (
    initiative_handle TEXT NOT NULL,
    artifact_handle TEXT NOT NULL,
    producer_task_handle TEXT NOT NULL,
    kind TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    source_revision TEXT NOT NULL,
    media_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    produced_at TEXT NOT NULL,
    supersedes_artifact_handle TEXT NOT NULL,
    content BLOB NOT NULL,
    PRIMARY KEY(initiative_handle, artifact_handle),
    FOREIGN KEY(initiative_handle) REFERENCES initiatives(handle),
    FOREIGN KEY(producer_task_handle) REFERENCES tasks(handle)
);
CREATE INDEX initiative_contract_artifacts_producer_idx
ON initiative_contract_artifacts(initiative_handle, producer_task_handle, kind);
INSERT INTO schema_migrations(version, applied_at)
VALUES (44, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

func insertInitiativeContractArtifact(
	ctx context.Context,
	target execer,
	prepared application.PreparedInitiativeContractArtifact,
) error {
	artifact := prepared.Artifact
	const statement = `INSERT INTO initiative_contract_artifacts (
        initiative_handle, artifact_handle, producer_task_handle, kind,
        content_hash, source_revision, media_type, size, produced_at,
        supersedes_artifact_handle, content
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement,
		artifact.InitiativeHandle, artifact.ArtifactHandle, artifact.ProducerTaskHandle,
		artifact.Kind, artifact.ContentHash, artifact.SourceRevision, artifact.MediaType,
		artifact.Size, formatTime(artifact.ProducedAt), artifact.SupersedesArtifactHandle,
		prepared.Content,
	)
	return err
}

func listInitiativeContractArtifacts(
	ctx context.Context,
	source queryer,
	initiativeHandle string,
) ([]domain.ComponentContractArtifact, error) {
	const query = `SELECT artifact_handle, initiative_handle, producer_task_handle,
        kind, content_hash, source_revision, media_type, size, produced_at,
        supersedes_artifact_handle, content
        FROM initiative_contract_artifacts
        WHERE (? = '' OR initiative_handle = ?)
        ORDER BY initiative_handle, artifact_handle`
	rows, err := source.QueryContext(ctx, query, initiativeHandle, initiativeHandle)
	if err != nil {
		return nil, fmt.Errorf("list initiative contract artifacts: %w", err)
	}
	defer rows.Close()
	artifacts := make([]domain.ComponentContractArtifact, 0)
	for rows.Next() {
		var artifact domain.ComponentContractArtifact
		var producedAtText string
		var content []byte
		if err := rows.Scan(
			&artifact.ArtifactHandle, &artifact.InitiativeHandle, &artifact.ProducerTaskHandle,
			&artifact.Kind, &artifact.ContentHash, &artifact.SourceRevision, &artifact.MediaType,
			&artifact.Size, &producedAtText, &artifact.SupersedesArtifactHandle, &content,
		); err != nil {
			return nil, fmt.Errorf("scan initiative contract artifact: %w", err)
		}
		artifact.ProducedAt, err = parseTime(producedAtText)
		if err != nil || artifact.Validate() != nil || int64(len(content)) != artifact.Size ||
			fmt.Sprintf("%x", sha256.Sum256(content)) != artifact.ContentHash {
			return nil, errors.New("stored initiative contract artifact is invalid")
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate initiative contract artifacts: %w", err)
	}
	return artifacts, nil
}

func validatePreparedInitiativeContractArtifacts(
	mutation application.PreparedInitiativeMutation,
	members map[string]domain.Task,
) error {
	listed := make(map[string]struct{}, len(mutation.Initiative.ContractArtifacts))
	for _, handle := range mutation.Initiative.ContractArtifacts {
		listed[handle] = struct{}{}
	}
	seen := make(map[string]struct{}, len(mutation.ContractArtifacts))
	byHandle := make(map[string]domain.ComponentContractArtifact, len(mutation.ContractArtifacts))
	for _, prepared := range mutation.ContractArtifacts {
		artifact := prepared.Artifact
		producer, found := members[artifact.ProducerTaskHandle]
		if artifact.Validate() != nil || artifact.InitiativeHandle != mutation.Initiative.Handle ||
			!artifact.ProducedAt.Equal(mutation.At) || !found ||
			artifact.SourceRevision != producer.BaseRevision || int64(len(prepared.Content)) != artifact.Size ||
			fmt.Sprintf("%x", sha256.Sum256(prepared.Content)) != artifact.ContentHash {
			return errors.New("commit prepared initiative: invalid contract artifact")
		}
		if _, current := listed[artifact.ArtifactHandle]; !current {
			return errors.New("commit prepared initiative: unlisted contract artifact")
		}
		if _, duplicate := seen[artifact.ArtifactHandle]; duplicate {
			return errors.New("commit prepared initiative: duplicate contract artifact")
		}
		seen[artifact.ArtifactHandle] = struct{}{}
		byHandle[artifact.ArtifactHandle] = artifact
	}
	if len(seen) != len(listed) {
		return errors.New("commit prepared initiative: contract artifact set is incomplete")
	}
	for _, task := range members {
		for _, pin := range task.ConsumedContracts {
			artifact, found := byHandle[pin.ArtifactHandle]
			if !found || artifact.Kind != pin.Kind || artifact.ContentHash != pin.ContentHash {
				return errors.New("commit prepared initiative: consumed contract is not exact")
			}
		}
	}
	for _, edge := range mutation.Initiative.Edges {
		if edge.Kind != domain.EdgeConsumesArtifact {
			continue
		}
		resolved := false
		for _, pin := range members[edge.ToTaskHandle].ConsumedContracts {
			artifact, found := byHandle[pin.ArtifactHandle]
			if found && artifact.ProducerTaskHandle == edge.FromTaskHandle &&
				artifact.Kind == edge.RequiredArtifactKind && pin.Kind == edge.RequiredArtifactKind &&
				artifact.ContentHash == pin.ContentHash {
				resolved = true
				break
			}
		}
		if !resolved {
			return errors.New("commit prepared initiative: artifact edge does not resolve to its producer")
		}
	}
	return nil
}
