// Package forge implements typed source-control forge delivery adapters.
package forge

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ErrPullRequestTruthUnavailable marks a temporary remote failure that is safe
// to retry without changing pull-request delivery authority.
var ErrPullRequestTruthUnavailable = errors.New("pull-request truth is temporarily unavailable")

// ErrPullRequestMergeOutcomeUnknown marks a merge mutation whose exact result
// or actual method could not be proved. Callers preserve unknown rather than
// translating intended or acknowledged state into success.
var ErrPullRequestMergeOutcomeUnknown = errors.New("pull-request merge outcome is unknown")

// CredentialKind is the closed forge authority vocabulary.
//
// Merge is a THIRD identity, not a wider push. It is resolved only inside the
// approved merge operation and never reaches a worker, so the ability to push a
// branch never carries the ability to merge it.
type CredentialKind string

const (
	CredentialRead  CredentialKind = "read"
	CredentialPush  CredentialKind = "push"
	CredentialMerge CredentialKind = "merge"
)

// CredentialScope is one operator-asserted least-privilege grant.
type CredentialScope string

const (
	ScopeContentsRead      CredentialScope = "contents:read"
	ScopeContentsWrite     CredentialScope = "contents:write"
	ScopePullRequestsRead  CredentialScope = "pull_requests:read"
	ScopeChecksRead        CredentialScope = "checks:read"
	ScopePullRequestsWrite CredentialScope = "pull_requests:write"
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

// PullRequestVerificationRequest re-reads one recorded delivery without push
// or pull-request creation authority.
type PullRequestVerificationRequest struct {
	Branch         string
	HeadRevision   string
	PullRequestID  string
	RequiredChecks []string
}

// PullRequestTruth contains only the re-read reference and typed evidence.
type PullRequestTruth struct {
	URL      string
	Evidence domain.ForgeEvidence
}

// MergeMethod is the operator-selected GitHub merge strategy.
type MergeMethod string

const (
	MergeCommit MergeMethod = "merge"
	MergeSquash MergeMethod = "squash"
	MergeRebase MergeMethod = "rebase"
)

// PullRequestMergeRequest binds one merge to the already-approved exact forge
// identity and every required check observed in its evidence bundle.
type PullRequestMergeRequest struct {
	OperationID    string
	Branch         string
	HeadRevision   string
	PullRequestID  string
	Method         MergeMethod
	RequiredChecks []string
}

// PullRequestMergeReceipt is post-mutation forge truth, not the API call's
// optimistic acknowledgement.
type PullRequestMergeReceipt struct {
	RepositoryID        string
	PullRequestID       string
	HeadRevision        string
	MergeCommitRevision string
	Method              MergeMethod
}
