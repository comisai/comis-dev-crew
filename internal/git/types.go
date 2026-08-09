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
}

// Repository is a validated immutable registry entry.
type Repository struct {
	ID                   string
	PrimaryCheckout      string
	WorktreeRoot         string
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
