package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ReadTaskContractArtifact returns only an artifact pinned by the exact task.
// The read snapshot verifies task membership, immutable pin metadata, and the
// stored bytes before returning content to a protected runtime attachment.
func (store *Store) ReadTaskContractArtifact(
	ctx context.Context,
	taskHandle string,
	artifactHandle string,
) (domain.ContractArtifactContent, error) {
	if ctx == nil {
		return domain.ContractArtifactContent{}, errors.New("read task contract artifact: context is required")
	}
	if domain.ValidateTaskHandle(taskHandle) != nil || domain.ValidateContractArtifactHandle(artifactHandle) != nil {
		return domain.ContractArtifactContent{}, errors.New("read task contract artifact: selector is invalid")
	}
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return domain.ContractArtifactContent{}, fmt.Errorf("begin task contract artifact read: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	content, err := readPinnedTaskContractArtifact(ctx, transaction, taskHandle, artifactHandle)
	if err != nil {
		return domain.ContractArtifactContent{}, err
	}
	if err := transaction.Commit(); err != nil {
		return domain.ContractArtifactContent{}, fmt.Errorf("commit task contract artifact read: %w", err)
	}
	content.Content = append([]byte(nil), content.Content...)
	return content, nil
}

func readPinnedTaskContractArtifact(
	ctx context.Context,
	source queryer,
	taskHandle string,
	artifactHandle string,
) (domain.ContractArtifactContent, error) {
	task, err := getTask(ctx, source, taskHandle)
	if err != nil {
		return domain.ContractArtifactContent{}, fmt.Errorf("read task contract artifact task: %w", err)
	}
	var pin *domain.PinnedContract
	for index := range task.ConsumedContracts {
		if task.ConsumedContracts[index].ArtifactHandle == artifactHandle {
			pin = &task.ConsumedContracts[index]
			break
		}
	}
	if pin == nil {
		return domain.ContractArtifactContent{}, fmt.Errorf("read task contract artifact: artifact is not pinned: %w", application.ErrNotFound)
	}
	initiatives, err := listInitiatives(ctx, source)
	if err != nil {
		return domain.ContractArtifactContent{}, fmt.Errorf("read task contract artifact initiatives: %w", err)
	}
	var containing *domain.DevelopmentInitiative
	for index := range initiatives {
		if !initiatives[index].ContainsTask(taskHandle) {
			continue
		}
		if containing != nil {
			return domain.ContractArtifactContent{}, errors.New("read task contract artifact: task belongs to multiple initiatives")
		}
		containing = &initiatives[index]
	}
	if containing == nil || !containsArtifactHandle(containing.ContractArtifacts, artifactHandle) {
		return domain.ContractArtifactContent{}, errors.New("read task contract artifact: pinned artifact inventory is unavailable")
	}
	content, err := getInitiativeContractArtifactContent(ctx, source, containing.Handle, artifactHandle)
	if err != nil {
		return domain.ContractArtifactContent{}, err
	}
	if content.Artifact.Kind != pin.Kind || content.Artifact.ContentHash != pin.ContentHash {
		return domain.ContractArtifactContent{}, errors.New("read task contract artifact: stored artifact differs from task pin")
	}
	return content, nil
}

func containsArtifactHandle(handles []string, expected string) bool {
	for _, handle := range handles {
		if handle == expected {
			return true
		}
	}
	return false
}

func getInitiativeContractArtifactContent(
	ctx context.Context,
	source queryer,
	initiativeHandle string,
	artifactHandle string,
) (domain.ContractArtifactContent, error) {
	const query = `SELECT artifact_handle, initiative_handle, producer_task_handle,
        kind, content_hash, source_revision, media_type, size, produced_at,
        supersedes_artifact_handle, content
        FROM initiative_contract_artifacts
        WHERE initiative_handle = ? AND artifact_handle = ?`
	var content domain.ContractArtifactContent
	var producedAtText string
	err := source.QueryRowContext(ctx, query, initiativeHandle, artifactHandle).Scan(
		&content.Artifact.ArtifactHandle, &content.Artifact.InitiativeHandle,
		&content.Artifact.ProducerTaskHandle, &content.Artifact.Kind,
		&content.Artifact.ContentHash, &content.Artifact.SourceRevision,
		&content.Artifact.MediaType, &content.Artifact.Size, &producedAtText,
		&content.Artifact.SupersedesArtifactHandle, &content.Content,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.ContractArtifactContent{}, fmt.Errorf("read task contract artifact: %w", application.ErrNotFound)
	}
	if err != nil {
		return domain.ContractArtifactContent{}, fmt.Errorf("scan task contract artifact: %w", err)
	}
	content.Artifact.ProducedAt, err = parseTime(producedAtText)
	if err != nil || content.Validate() != nil {
		return domain.ContractArtifactContent{}, errors.New("stored task contract artifact is invalid")
	}
	return content, nil
}
