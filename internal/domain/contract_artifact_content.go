package domain

import (
	"crypto/sha256"
	"errors"
	"fmt"
)

// ContractArtifactContent pairs immutable artifact metadata with the exact
// bounded bytes named by its digest.
type ContractArtifactContent struct {
	Artifact ComponentContractArtifact
	Content  []byte
}

// Validate rejects metadata/content disagreement before artifact bytes cross
// a trust boundary.
func (content ContractArtifactContent) Validate() error {
	if err := content.Artifact.Validate(); err != nil {
		return err
	}
	if int64(len(content.Content)) != content.Artifact.Size {
		return errors.New("contract artifact content size differs")
	}
	if fmt.Sprintf("%x", sha256.Sum256(content.Content)) != content.Artifact.ContentHash {
		return errors.New("contract artifact content digest differs")
	}
	return nil
}
