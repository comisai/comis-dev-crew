package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func (registry *Registry) completeRebaseRecoveryInIsolation(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, error) {
	proof, found, err := registry.serverRebaseProof(repository, request)
	if err != nil || !found || proof.operationID != originalIntegrationOperationID(request) {
		return "", errors.New("apply integration candidate: recovery server proof is unavailable")
	}
	if proof.resultingHead != "" {
		if err := registry.requireServerRebaseProof(ctx, repository, request, proof.resultingHead); err != nil {
			return "", err
		}
		return proof.resultingHead, nil
	}
	rebaseHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil {
		return "", errors.New("apply integration candidate: recovery conflict identity is unavailable")
	}
	continued, _, conflicts, err := registry.currentServerRebasePrefix(ctx, repository, request, proof, rebaseHead)
	if err != nil || len(continued) >= len(proof.candidateCommits) || proof.candidateCommits[len(continued)] != rebaseHead {
		return "", errors.New("apply integration candidate: recovery prefix differs")
	}
	conflict, found := serverRebaseConflictForCommit(conflicts, rebaseHead)
	if !found || conflict.resolvedTree == "" {
		return "", errors.New("apply integration candidate: recovery resolution proof is unavailable")
	}
	currentHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", errors.New("apply integration candidate: recovery parent is unavailable")
	}
	directory, _, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return "", err
	}
	var resultingHead string
	err = registry.withIsolatedRebaseWorkspace(ctx, request.Target.WorktreePath, directory,
		func(workspace gitWorkspaceEnvironment) error {
			branch := "refs/heads/recovery-result"
			resolved, err := registry.writeIsolatedResolvedRebaseCommit(
				ctx, repository, workspace, rebaseHead, currentHead, conflict.resolvedTree,
			)
			if err != nil {
				return err
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"update-ref", branch, resolved); err != nil {
				return errors.New("apply integration candidate: isolated recovery head is unavailable")
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"symbolic-ref", "HEAD", branch); err != nil {
				return errors.New("apply integration candidate: isolated recovery attachment is unavailable")
			}
			if _, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, workspace,
				"reset", "--hard", resolved); err != nil {
				return errors.New("apply integration candidate: isolated recovery checkout is unavailable")
			}
			for _, candidate := range proof.candidateCommits[len(continued)+1:] {
				arguments := append(isolatedIntegrationMutationConfig(),
					"cherry-pick", "--allow-empty", "--keep-redundant-commits", candidate,
				)
				_, exitCode, err := executeGitWithEnvironmentAndOutputLimit(
					ctx, registry.gitExecutable, &workspace, maximumGitOutputBytes, arguments...,
				)
				if err != nil || exitCode != 0 {
					return errors.New("apply integration candidate: isolated recovery sequence is not complete")
				}
			}
			resultingHead, err = runGitInWorkspace(ctx, registry.gitExecutable, workspace,
				"rev-parse", "--verify", "HEAD^{commit}")
			if err != nil || !gitRevisionPattern.MatchString(resultingHead) {
				return errors.New("apply integration candidate: isolated recovery result is unavailable")
			}
			if err := registry.validateIsolatedMaterializationSnapshot(ctx, workspace, resultingHead); err != nil {
				return err
			}
			return importIsolatedGitObjects(workspace.gitObjectDirectory, workspace.gitAlternateObjectDirectory)
		})
	if err != nil {
		return "", err
	}
	if err := registry.completeServerRebaseProof(ctx, repository, request, resultingHead); err != nil {
		return "", err
	}
	return resultingHead, nil
}

func (registry *Registry) writeIsolatedResolvedRebaseCommit(
	ctx context.Context,
	repository Repository,
	workspace gitWorkspaceEnvironment,
	candidate string,
	parent string,
	tree string,
) (string, error) {
	content, err := registry.rebaseCommitContent(ctx, repository, candidate)
	if err != nil {
		return "", err
	}
	authorFields := strings.Fields(content.author)
	if len(authorFields) < 3 || !gitRevisionPattern.MatchString(parent) || !gitRevisionPattern.MatchString(tree) {
		return "", errors.New("apply integration candidate: resolved commit identity is invalid")
	}
	var raw bytes.Buffer
	_, _ = fmt.Fprintf(&raw, "tree %s\nparent %s\nauthor %s\ncommitter DevCrew Integration <integration@example.invalid> %s %s\n\n",
		tree, parent, content.author, authorFields[len(authorFields)-2], authorFields[len(authorFields)-1])
	_, _ = raw.Write(content.message)
	output, exitCode, err := executeGitWithEnvironmentInputAndOutputLimit(
		ctx, registry.gitExecutable, &workspace, raw.Bytes(), 128, "hash-object", "-t", "commit", "-w", "--stdin",
	)
	result := strings.TrimSpace(string(output))
	if err != nil || exitCode != 0 || !gitRevisionPattern.MatchString(result) {
		return "", errors.New("apply integration candidate: resolved commit could not be isolated")
	}
	return result, nil
}
