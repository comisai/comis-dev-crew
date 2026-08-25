package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strconv"
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
	resolvedTree, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"write-tree")
	if err != nil || !gitRevisionPattern.MatchString(resolvedTree) {
		return errors.New("apply integration candidate: conflict resolution tree is unavailable")
	}
	want := proof
	want.conflicts = append([]serverRebaseConflict(nil), proof.conflicts...)
	for index := range want.conflicts {
		if want.conflicts[index].commit != rebaseHead {
			continue
		}
		if want.conflicts[index].resolvedTree != "" && want.conflicts[index].resolvedTree != resolvedTree {
			return errors.New("apply integration candidate: conflict resolution proof differs")
		}
		want.conflicts[index].resolvedTree = resolvedTree
		if sameServerRebaseProof(want, proof) {
			return nil
		}
		directory, _, pathErr := serverRebaseProofPath(repository, request)
		if pathErr != nil {
			return pathErr
		}
		return replaceServerRebaseProof(directory, path, proof, want)
	}
	return errors.New("apply integration candidate: conflict resolution snapshot is unavailable")
}

type rebaseCommitContent struct {
	tree      string
	parent    string
	author    string
	committer string
	message   []byte
}

func (registry *Registry) validateRebaseConflictResult(
	ctx context.Context,
	repository Repository,
	candidate string,
	parent string,
	tree string,
	result string,
) error {
	candidateContent, err := registry.rebaseCommitContent(ctx, repository, candidate)
	if err != nil {
		return err
	}
	resultContent, err := registry.rebaseCommitContent(ctx, repository, result)
	if err != nil {
		return err
	}
	committer := strings.TrimPrefix(resultContent.committer,
		"DevCrew Integration <integration@example.invalid> ")
	committerFields := strings.Fields(committer)
	if resultContent.tree != tree || resultContent.parent != parent ||
		resultContent.author != candidateContent.author || !bytes.Equal(resultContent.message, candidateContent.message) ||
		len(committerFields) != 2 || !validRebaseCommitTime(committerFields[0], committerFields[1]) {
		return errors.New("apply integration candidate: continued conflict result differs")
	}
	return nil
}

func (registry *Registry) rebaseCommitContent(
	ctx context.Context,
	repository Repository,
	revision string,
) (rebaseCommitContent, error) {
	raw, err := runGitBytesWithLimit(ctx, maximumRebasePatchBytes, registry.gitExecutable,
		"--no-optional-locks", "-C", repository.PrimaryCheckout, "cat-file", "commit", revision)
	if err != nil {
		return rebaseCommitContent{}, errors.New("apply integration candidate: conflict commit identity is unavailable")
	}
	separator := bytes.Index(raw, []byte("\n\n"))
	if separator < 1 {
		return rebaseCommitContent{}, errors.New("apply integration candidate: conflict commit identity is invalid")
	}
	var content rebaseCommitContent
	counts := map[string]int{}
	for _, line := range strings.Split(string(raw[:separator]), "\n") {
		name, value, found := strings.Cut(line, " ")
		if !found {
			return rebaseCommitContent{}, errors.New("apply integration candidate: conflict commit identity is invalid")
		}
		counts[name]++
		switch name {
		case "tree":
			content.tree = value
		case "parent":
			content.parent = value
		case "author":
			content.author = value
		case "committer":
			content.committer = value
		default:
			return rebaseCommitContent{}, errors.New("apply integration candidate: conflict commit headers are unsupported")
		}
	}
	if counts["tree"] != 1 || counts["parent"] != 1 || counts["author"] != 1 || counts["committer"] != 1 {
		return rebaseCommitContent{}, errors.New("apply integration candidate: conflict commit identity is invalid")
	}
	content.message = append([]byte(nil), raw[separator+2:]...)
	return content, nil
}

func validRebaseCommitTime(timestamp, timezone string) bool {
	if _, err := strconv.ParseInt(timestamp, 10, 64); err != nil {
		return false
	}
	if len(timezone) != 5 || timezone[0] != '+' && timezone[0] != '-' {
		return false
	}
	_, err := strconv.ParseUint(timezone[1:], 10, 16)
	return err == nil
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
			left[index].resolvedTree != right[index].resolvedTree ||
			left[index].expectedResult != right[index].expectedResult ||
			!sameRebaseCommits(left[index].paths, right[index].paths) {
			return false
		}
	}
	return true
}

func validServerRebaseConflictBindings(resolved []string, conflicts []serverRebaseConflict) bool {
	byCommit := make(map[string]serverRebaseConflict, len(conflicts))
	for _, conflict := range conflicts {
		if _, duplicate := byCommit[conflict.commit]; duplicate ||
			conflict.resolvedTree == "" && conflict.expectedResult != "" {
			return false
		}
		byCommit[conflict.commit] = conflict
	}
	for _, commit := range resolved {
		conflict, found := byCommit[commit]
		if !found || conflict.expectedResult == "" {
			return false
		}
	}
	return true
}
