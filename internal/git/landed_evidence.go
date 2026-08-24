package git

import (
	"context"
	"errors"
	"sort"
	"strings"
)

const maximumRemoteTrackingRefs = 256

// ReachableRemoteRefs returns bounded remote-tracking refs that contain one exact commit.
func (registry *Registry) ReachableRemoteRefs(
	ctx context.Context,
	repositoryID string,
	headRevision string,
) ([]string, error) {
	if registry == nil || ctx == nil {
		return nil, errors.New("inspect remote reachability: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !repositoryIDPattern.MatchString(repositoryID) || !gitRevisionPattern.MatchString(headRevision) {
		return nil, errors.New("inspect remote reachability: request identity is invalid")
	}
	repository, err := registry.Resolve(repositoryID)
	if err != nil {
		return nil, errors.New("inspect remote reachability: repository is unavailable")
	}
	commit, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-parse", "--verify", headRevision+"^{commit}")
	if err != nil || commit != headRevision {
		return nil, errors.New("inspect remote reachability: commit is unavailable")
	}
	encoded, err := runGitBytesWithLimit(ctx, 1<<20, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout,
		"for-each-ref", "--contains="+headRevision, "--format=%(refname:short)", "refs/remotes/")
	if err != nil {
		return nil, errors.New("inspect remote reachability: Git query failed")
	}
	lines := strings.Split(strings.TrimSuffix(string(encoded), "\n"), "\n")
	refs := make([]string, 0, len(lines))
	seen := make(map[string]struct{}, len(lines))
	for _, reference := range lines {
		if reference == "" {
			continue
		}
		if len(reference) > 255 || !strings.Contains(reference, "/") || strings.ContainsAny(reference, " \t\r\x00") {
			return nil, errors.New("inspect remote reachability: Git output is invalid")
		}
		if _, duplicate := seen[reference]; duplicate {
			return nil, errors.New("inspect remote reachability: Git output is ambiguous")
		}
		seen[reference] = struct{}{}
		refs = append(refs, reference)
		if len(refs) > maximumRemoteTrackingRefs {
			return nil, errors.New("inspect remote reachability: ref count exceeds the configured bound")
		}
	}
	sort.Strings(refs)
	return refs, nil
}
