// Package forge implements typed source-control forge delivery adapters.
package forge

import (
	"context"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// CredentialKind is the closed non-merge E0 forge authority vocabulary.
type CredentialKind string

const (
	CredentialRead CredentialKind = "read"
	CredentialPush CredentialKind = "push"
)

// CredentialScope is one operator-asserted least-privilege grant.
type CredentialScope string

const (
	ScopeContentsRead     CredentialScope = "contents:read"
	ScopeContentsWrite    CredentialScope = "contents:write"
	ScopePullRequestsRead CredentialScope = "pull_requests:read"
	ScopeChecksRead       CredentialScope = "checks:read"
)

// Credential is resolved only within one adapter operation and is never logged.
type Credential struct {
	Kind   CredentialKind
	Secret string
	Scopes []CredentialScope
}

// CredentialSource resolves one separately configured forge identity.
type CredentialSource interface {
	Resolve(context.Context) (Credential, error)
}

// BranchPushRequest binds a push to one verified worktree branch and exact head.
type BranchPushRequest struct {
	OperationID  string
	WorktreePath string
	Branch       string
	HeadRevision string
}

// BranchPusher performs only the reviewed exact-branch transfer.
type BranchPusher interface {
	Push(context.Context, Credential, BranchPushRequest) error
}

// PullRequestRequest is the complete server-owned E0 delivery input.
type PullRequestRequest struct {
	OperationID    string
	WorktreePath   string
	Branch         string
	HeadRevision   string
	Title          string
	RequiredChecks []string
}

// PullRequestTruth contains only the re-read reference and typed evidence.
type PullRequestTruth struct {
	URL      string
	Evidence domain.ForgeEvidence
}
