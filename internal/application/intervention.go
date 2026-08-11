package application

import (
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// HandbackAction is the closed E0 intervention result requested after a worker
// has reported a safe pause and released its terminal.
type HandbackAction string

const (
	HandbackValidateDeveloperWork HandbackAction = "validate-developer-work"
)

// WorkspaceCleanliness is the fresh tracked and untracked worktree posture.
type WorkspaceCleanliness string

const (
	WorkspaceClean WorkspaceCleanliness = "clean"
	WorkspaceDirty WorkspaceCleanliness = "dirty"
)

// WorkspaceSnapshot is independently observed Git identity captured at
// handback. It contains no diff or repository content.
type WorkspaceSnapshot struct {
	TaskHandle   string               `json:"taskHandle"`
	RepositoryID string               `json:"repositoryId"`
	WorktreePath string               `json:"worktreePath"`
	Branch       string               `json:"branch"`
	HeadRevision string               `json:"headRevision"`
	Cleanliness  WorkspaceCleanliness `json:"cleanliness"`
}

// Validate rejects incomplete, escaped, or content-bearing snapshot fields.
func (snapshot WorkspaceSnapshot) Validate() error {
	if domain.ValidateTaskHandle(snapshot.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("repositoryId", snapshot.RepositoryID) != nil ||
		domain.ValidateGitRevision(snapshot.HeadRevision) != nil ||
		!filepath.IsAbs(snapshot.WorktreePath) || filepath.Clean(snapshot.WorktreePath) != snapshot.WorktreePath ||
		snapshot.Branch == "" || len(snapshot.Branch) > 255 || strings.ContainsAny(snapshot.Branch, "\x00\r\n\t ") ||
		(snapshot.Cleanliness != WorkspaceClean && snapshot.Cleanliness != WorkspaceDirty) {
		return errors.New("workspace snapshot is invalid")
	}
	return nil
}

// TaskHandbackMutation is the exact durable paused-to-validation transaction.
type TaskHandbackMutation struct {
	OperationID   string
	SubjectDigest string
	TaskHandle    string
	Action        HandbackAction
	Snapshot      WorkspaceSnapshot
	At            time.Time
}
