// Package git validates operator-configured repositories and real task
// worktrees without granting worker-launch or mutation authority.
package git

import "errors"

// ErrRepositoryNotFound means an opaque repository ID is not configured.
var ErrRepositoryNotFound = errors.New("repository is not configured")

// ErrCandidateWorktreeUnverified identifies task-scoped structural Git evidence
// that cannot safely be treated as a candidate snapshot.
var ErrCandidateWorktreeUnverified = errors.New("candidate worktree is unverified")

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

// CandidateDiffRequest names one task's own verified worktree and the revision
// its work is measured from.
type CandidateDiffRequest struct {
	TaskHandle   string
	RepositoryID string
	WorktreePath string
	BaseRevision string
}

// CandidateFileChange is one changed path with its numeric extent. A binary
// change carries no counts, so it is marked rather than reported as empty.
type CandidateFileChange struct {
	Path         string `json:"path"`
	PreviousPath string `json:"previousPath,omitempty"`
	Added        int    `json:"added"`
	Deleted      int    `json:"deleted"`
	Binary       bool   `json:"binary,omitempty"`
}

// CandidateDiffTotals is the bounded extent of one change set.
type CandidateDiffTotals struct {
	Files       int `json:"files"`
	Added       int `json:"added"`
	Deleted     int `json:"deleted"`
	BinaryFiles int `json:"binaryFiles"`
}

// CandidateDiff is the bounded machine-readable summary of one task's work.
type CandidateDiff struct {
	RepositoryID      string                `json:"repositoryId"`
	WorktreePath      string                `json:"worktreePath"`
	BaseRevision      string                `json:"baseRevision"`
	HeadRevision      string                `json:"headRevision"`
	Committed         []CandidateFileChange `json:"committed,omitempty"`
	Uncommitted       []CandidateFileChange `json:"uncommitted,omitempty"`
	CommittedTotals   CandidateDiffTotals   `json:"committedTotals"`
	UncommittedTotals CandidateDiffTotals   `json:"uncommittedTotals"`
	// FileListTruncated states that the change set was larger than this read
	// bounds. The totals still describe the listed rows, so a truncated listing
	// never reads as a complete one.
	FileListTruncated bool `json:"fileListTruncated,omitempty"`
}

// CandidateSnapshot is current machine-readable Git truth.
type CandidateSnapshot struct {
	RepositoryID string
	WorktreePath string
	Branch       string
	HeadRevision string
	Cleanliness  CandidateCleanliness
}

// FixtureCandidateRequest binds one deterministic test artifact to an exact
// prepared task worktree and pinned base revision.
type FixtureCandidateRequest struct {
	TaskHandle           string
	RepositoryID         string
	WorktreePath         string
	BaseRevision         string
	ArtifactRelativePath string
}
