package git

import (
	"context"
	"errors"
	"strings"
)

// PrimarySyncOutcome is the closed result of one primary-checkout sync.
type PrimarySyncOutcome string

const (
	PrimarySyncUpdated        PrimarySyncOutcome = "updated"
	PrimarySyncAlreadyCurrent PrimarySyncOutcome = "already_current"
	PrimarySyncRefused        PrimarySyncOutcome = "refused"
)

// PrimarySyncRefusal names the exact posture that refused a synchronization.
//
// The reason is part of the answer, not decoration: a dirty checkout is
// repaired by committing or stashing, a divergent one by the developer deciding
// what to do with their own commits, and a detached or non-default checkout by
// returning to the branch they meant to be on. A single "not ready" would leave
// every one of those to guesswork.
type PrimarySyncRefusal string

const (
	PrimarySyncRefusalDirty          PrimarySyncRefusal = "dirty_checkout"
	PrimarySyncRefusalDivergent      PrimarySyncRefusal = "divergent_history"
	PrimarySyncRefusalDetached       PrimarySyncRefusal = "detached_head"
	PrimarySyncRefusalNonDefault     PrimarySyncRefusal = "non_default_branch"
	PrimarySyncRefusalUpstreamAbsent PrimarySyncRefusal = "upstream_absent"
	PrimarySyncRefusalAmbiguous      PrimarySyncRefusal = "ambiguous_state"
)

// PrimarySyncRequest names the configured repository whose primary checkout is
// being synchronized. It carries no path: the checkout is resolved from
// operator configuration so a caller cannot aim the update at another tree.
type PrimarySyncRequest struct {
	RepositoryID string
}

// PrimarySyncResult reports what happened without leaking repository content.
type PrimarySyncResult struct {
	RepositoryID string
	Branch       string
	PreviousHead string
	Head         string
	Outcome      PrimarySyncOutcome
	Refusal      PrimarySyncRefusal
}

// SynchronizePrimary fast-forwards one configured primary checkout, or refuses.
//
// No task ever mutates the primary checkout; this is the single sanctioned
// exception and it is deliberately the weakest operation that is still useful.
// It only ever fast-forwards: the primary is the developer's own working copy,
// so a reset, clean, stash, checkout or force-update here would destroy work
// that belongs to nobody but them and that no task could restore. Every posture
// it cannot advance safely is refused by name and leaves the checkout untouched.
func (registry *Registry) SynchronizePrimary(
	ctx context.Context,
	request PrimarySyncRequest,
) (PrimarySyncResult, error) {
	if ctx == nil {
		return PrimarySyncResult{}, errors.New("synchronize primary checkout: context is required")
	}
	if err := ctx.Err(); err != nil {
		return PrimarySyncResult{}, err
	}
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return PrimarySyncResult{}, err
	}

	head, err := registry.primaryGit(ctx, repository, "rev-parse", "HEAD")
	if err != nil {
		return PrimarySyncResult{}, err
	}
	result := PrimarySyncResult{
		RepositoryID: repository.ID, PreviousHead: head, Head: head,
	}

	// Branch identity first. A detached head has no branch to advance, and a
	// non-default branch is the developer working somewhere on purpose.
	branch, err := registry.primaryGit(ctx, repository, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch == "" {
		return refusePrimarySync(result, PrimarySyncRefusalDetached), nil
	}
	result.Branch = branch
	if branch != repository.DefaultBranch {
		return refusePrimarySync(result, PrimarySyncRefusalNonDefault), nil
	}

	// Dirty includes untracked files. A fast-forward would not touch them, but
	// it changes the tree underneath edits the developer has not yet committed,
	// and the point of this tool is that it never surprises them.
	status, err := registry.primaryGitBytes(ctx, repository, "status", "--porcelain", "--untracked-files=normal")
	if err != nil {
		return PrimarySyncResult{}, err
	}
	if strings.TrimSpace(string(status)) != "" {
		return refusePrimarySync(result, PrimarySyncRefusalDirty), nil
	}

	upstream, err := registry.primaryGit(ctx, repository, "rev-parse", "--symbolic-full-name", "--abbrev-ref", "@{upstream}")
	if err != nil || upstream == "" || strings.Contains(upstream, " ") {
		return refusePrimarySync(result, PrimarySyncRefusalUpstreamAbsent), nil
	}
	remote, _, found := strings.Cut(upstream, "/")
	if !found || remote == "" {
		return refusePrimarySync(result, PrimarySyncRefusalAmbiguous), nil
	}

	// Fetch is the only network step and it writes no working-tree state.
	if _, err := registry.primaryGitBytes(ctx, repository, "fetch", "--no-tags", "--prune", remote, branch); err != nil {
		return PrimarySyncResult{}, err
	}

	target, err := registry.primaryGit(ctx, repository, "rev-parse", upstream)
	if err != nil {
		return PrimarySyncResult{}, err
	}
	if target == head {
		result.Outcome = PrimarySyncAlreadyCurrent
		return result, nil
	}
	// Ancestry is what makes this a fast-forward. Anything else means the
	// checkout carries commits the upstream does not, and only the developer
	// can decide what becomes of them.
	fastForward, err := gitPredicate(ctx, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "merge-base", "--is-ancestor", head, target)
	if err != nil {
		return PrimarySyncResult{}, err
	}
	if !fastForward {
		return refusePrimarySync(result, PrimarySyncRefusalDivergent), nil
	}

	if _, err := registry.primaryGitBytes(ctx, repository, "merge", "--ff-only", target); err != nil {
		return PrimarySyncResult{}, err
	}
	updated, err := registry.primaryGit(ctx, repository, "rev-parse", "HEAD")
	if err != nil {
		return PrimarySyncResult{}, err
	}
	// A fast-forward that did not land where it aimed is not a success. The
	// checkout is reported as it actually is rather than as it was asked to be.
	if updated != target {
		return refusePrimarySync(result, PrimarySyncRefusalAmbiguous), nil
	}
	result.Head, result.Outcome = updated, PrimarySyncUpdated
	return result, nil
}

// primaryGit runs one fixed-argv Git command in the configured primary
// checkout. There is no shell and no caller-supplied executable or path.
func (registry *Registry) primaryGit(
	ctx context.Context,
	repository Repository,
	arguments ...string,
) (string, error) {
	return runGit(ctx, registry.gitExecutable,
		append([]string{"--no-optional-locks", "-C", repository.PrimaryCheckout}, arguments...)...)
}

// primaryGitBytes runs one fixed-argv Git command whose output is empty or
// multi-line, so it cannot go through the single-line inspection reader.
func (registry *Registry) primaryGitBytes(
	ctx context.Context,
	repository Repository,
	arguments ...string,
) ([]byte, error) {
	return runGitBytes(ctx, registry.gitExecutable,
		append([]string{"--no-optional-locks", "-C", repository.PrimaryCheckout}, arguments...)...)
}

func refusePrimarySync(result PrimarySyncResult, refusal PrimarySyncRefusal) PrimarySyncResult {
	result.Outcome, result.Refusal = PrimarySyncRefused, refusal
	return result
}
