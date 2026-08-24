package git

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) rebaseProtectedIndexDigest(
	ctx context.Context,
	worktreePath string,
	conflictPaths []string,
) (string, error) {
	allowed := make(map[string]struct{}, len(conflictPaths))
	for _, path := range conflictPaths {
		allowed[path] = struct{}{}
	}
	index, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", worktreePath, "ls-files", "--stage", "-z")
	if err != nil {
		return "", errors.New("apply integration candidate: conflicted index snapshot is unavailable")
	}
	hash := sha256.New()
	for _, encoded := range strings.Split(string(index), "\x00") {
		if encoded == "" {
			continue
		}
		separator := strings.IndexByte(encoded, '\t')
		if separator < 1 || separator == len(encoded)-1 {
			return "", errors.New("apply integration candidate: conflicted index snapshot is invalid")
		}
		if _, mutable := allowed[encoded[separator+1:]]; mutable {
			continue
		}
		_, _ = hash.Write([]byte(encoded))
		_, _ = hash.Write([]byte{0})
	}
	changed, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", worktreePath, "diff", "--name-only", "-z")
	if err != nil || !rebasePathsWithin(changed, allowed) {
		return "", errors.New("apply integration candidate: conflict changed a protected path")
	}
	untracked, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", worktreePath, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil || len(untracked) != 0 {
		return "", errors.New("apply integration candidate: conflict contains untracked paths")
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func rebasePathsWithin(encoded []byte, allowed map[string]struct{}) bool {
	for _, path := range strings.Split(string(encoded), "\x00") {
		if path == "" {
			continue
		}
		if _, found := allowed[path]; !found {
			return false
		}
	}
	return true
}

func (registry *Registry) validateServerRebaseConflictResolution(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) error {
	_, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil || !found || proof.operationID != originalIntegrationOperationID(request) {
		return errors.New("apply integration candidate: conflict server proof is unavailable")
	}
	rebaseHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil {
		return errors.New("apply integration candidate: conflict recovery identity is unavailable")
	}
	if err := registry.requireServerRebasePrefix(ctx, repository, request, proof, rebaseHead); err != nil {
		return err
	}
	var snapshot *serverRebaseConflict
	for index := range proof.conflicts {
		if proof.conflicts[index].commit == rebaseHead {
			snapshot = &proof.conflicts[index]
			break
		}
	}
	if snapshot == nil {
		return errors.New("apply integration candidate: conflict server snapshot is unavailable")
	}
	digest, err := registry.rebaseProtectedIndexDigest(ctx, request.Target.WorktreePath, snapshot.paths)
	if err != nil || digest != snapshot.indexDigest {
		return errors.New("apply integration candidate: conflict changed a protected path")
	}
	changed, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", request.Target.WorktreePath, "diff", "--name-only", "-z")
	if err != nil || len(changed) != 0 {
		return errors.New("apply integration candidate: conflict resolution is not fully staged")
	}
	return nil
}

func serverRebaseConflictsWereResolved(resolved []string, conflicts []serverRebaseConflict) bool {
	wanted := make(map[string]struct{}, len(resolved))
	for _, commit := range resolved {
		wanted[commit] = struct{}{}
	}
	for _, conflict := range conflicts {
		if _, found := wanted[conflict.commit]; !found {
			return false
		}
	}
	return true
}

func sameServerRebaseConflicts(left, right []serverRebaseConflict) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].commit != right[index].commit || left[index].indexDigest != right[index].indexDigest ||
			!sameRebaseCommits(left[index].paths, right[index].paths) {
			return false
		}
	}
	return true
}
