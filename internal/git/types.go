// Package git validates operator-configured repositories and real task
// worktrees without granting worker-launch or mutation authority.
package git

import "errors"

// ErrRepositoryNotFound means an opaque repository ID is not configured.
var ErrRepositoryNotFound = errors.New("repository is not configured")

// RegistryConfig is the complete operator-owned repository registry input.
type RegistryConfig struct {
	GitExecutable string
	ApprovedRoots []string
	Repositories  []RepositoryConfig
}

// RepositoryConfig maps one opaque ID to its primary checkout and dedicated
// task-worktree root.
type RepositoryConfig struct {
	ID              string
	PrimaryCheckout string
	WorktreeRoot    string
	DefaultBranch   string
}

// Repository is a validated immutable registry entry.
type Repository struct {
	ID                   string
	PrimaryCheckout      string
	WorktreeRoot         string
	DefaultBranch        string
	GitCommonDir         string
	GitCommonDirIdentity string
}

// Worktree is one validated task worktree belonging to a configured repository.
type Worktree struct {
	RepositoryID         string
	CanonicalPath        string
	GitCommonDir         string
	GitCommonDirIdentity string
}

// PrepareWorktreeRequest binds one deterministic task workspace to its stable
// preparation operation and exact pinned base.
type PrepareWorktreeRequest struct {
	OperationID    string
	TaskHandle     string
	RepositoryID   string
	BaseRevision   string
	LiveLeasePaths []string
}

// PreparedWorktree is the verified immutable identity returned for both a new
// worktree and an exact safe adoption.
type PreparedWorktree struct {
	OperationID          string
	TaskHandle           string
	RepositoryID         string
	CanonicalPath        string
	Branch               string
	BaseRevision         string
	HeadRevision         string
	GitCommonDir         string
	GitCommonDirIdentity string
}

// CleanupWorktreeRequest identifies the only clean prepared workspace that
// may be removed. Any changed or ambiguous evidence is preserved.
type CleanupWorktreeRequest struct {
	OperationID    string
	TaskHandle     string
	RepositoryID   string
	BaseRevision   string
	LiveLeasePaths []string
}

// DeliveredWorktreeCleanupRequest identifies one already-delivered candidate
// by the preparation that created it and its final immutable Git identity.
type DeliveredWorktreeCleanupRequest struct {
	PreparationOperationID string
	TaskHandle             string
	RepositoryID           string
	WorktreePath           string
	Branch                 string
	HeadRevision           string
}

// CandidateCleanliness is the closed candidate worktree posture.
type CandidateCleanliness string

const (
	CandidateClean CandidateCleanliness = "clean"
	CandidateDirty CandidateCleanliness = "dirty"
)

// CandidateSnapshotRequest binds inspection to one configured task root.
type CandidateSnapshotRequest struct {
	TaskHandle   string
	RepositoryID string
	WorktreePath string
}

// CandidateSnapshot is current machine-readable Git truth.
type CandidateSnapshot struct {
	RepositoryID string
	WorktreePath string
	Branch       string
	HeadRevision string
	Cleanliness  CandidateCleanliness
}
