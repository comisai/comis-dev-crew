package git

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const maximumRebaseProofCommits = 4096

type serverRebaseProof struct {
	candidateCommits []string
	resultingHead    string
}

func (registry *Registry) runIntegrationStrategy(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) error {
	if request.Strategy == application.IntegrationRebase {
		return registry.runRebaseIntegration(ctx, request, repository)
	}
	arguments := []string{
		"--no-optional-locks", "-C", request.Target.WorktreePath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
	}
	switch request.Strategy {
	case application.IntegrationMerge:
		arguments = append(arguments, "merge", "--no-ff", "--no-edit", "--no-verify", "--no-stat", request.Candidate.HeadRevision)
	case application.IntegrationCherryPick:
		arguments = append(arguments, "cherry-pick", request.Candidate.BaseRevision+".."+request.Candidate.HeadRevision)
	default:
		return errors.New("apply integration candidate: strategy is invalid")
	}
	_, err := runGitBytes(ctx, registry.gitExecutable, arguments...)
	return err
}

func (registry *Registry) runRebaseIntegration(
	ctx context.Context,
	request application.IntegrationAdapterRequest,
	repository Repository,
) error {
	targetRef, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "--quiet", "HEAD")
	if err != nil || targetRef != "refs/heads/"+expectedIntegrationTargetBranch(request) {
		return errors.New("apply integration candidate: target branch identity is unavailable")
	}
	if err := registry.prepareServerRebaseProof(ctx, repository, request); err != nil {
		return err
	}
	if err := registry.recordIntegrationTargetRef(ctx, request, targetRef); err != nil {
		return err
	}
	if err := registry.recordIntegrationRebaseProof(ctx, request); err != nil {
		return err
	}
	configuration := []string{
		"--no-optional-locks", "-C", request.Target.WorktreePath,
		"-c", "core.hooksPath=/dev/null", "-c", "commit.gpgSign=false",
		"-c", "user.name=DevCrew Integration", "-c", "user.email=integration@example.invalid",
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, append(configuration,
		"rebase", "--no-autostash", "--no-stat", "--onto", request.Target.ExpectedHead,
		request.Candidate.BaseRevision,
		strings.TrimPrefix(integrationRebaseProofRef(request), "refs/heads/"))...); err != nil {
		return err
	}
	resultingHead, err := registry.completeServiceRebase(ctx, repository, request)
	if err != nil {
		return err
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"update-ref", targetRef, resultingHead, request.Target.ExpectedHead); err != nil {
		return errors.New("apply integration candidate: target branch changed during rebase")
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.Target.WorktreePath,
		"symbolic-ref", "HEAD", targetRef); err != nil {
		return errors.New("apply integration candidate: rebased target could not be reattached")
	}
	if err := registry.retireIntegrationRebaseProof(ctx, request, resultingHead); err != nil {
		return err
	}
	return nil
}

func (registry *Registry) prepareServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) error {
	commits, err := registry.rebaseCommitRange(
		ctx, repository, request.Candidate.BaseRevision, request.Candidate.HeadRevision,
	)
	if err != nil {
		return errors.New("apply integration candidate: rebase range proof is unavailable")
	}
	directory, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	if err := ensureServerRebaseProofDirectory(repository.WorktreeRoot, directory); err != nil {
		return err
	}
	want := serverRebaseProof{candidateCommits: commits}
	existing, found, err := readServerRebaseProof(path)
	if err != nil {
		return err
	}
	if found {
		if !sameRebaseCommits(existing.candidateCommits, want.candidateCommits) {
			return errors.New("apply integration candidate: rebase range proof differs")
		}
		return syncDirectory(directory)
	}
	if err := createServerRebaseProof(path, encodeServerRebaseProof(want)); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func (registry *Registry) completeServiceRebase(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, error) {
	resultingHead, err := registry.inspectRecoveredRebaseHead(ctx, request)
	if err != nil {
		return "", err
	}
	if err := registry.completeServerRebaseProof(ctx, repository, request, resultingHead); err != nil {
		return "", err
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return "", err
	}
	return resultingHead, nil
}

func (registry *Registry) validRecoveredRebaseHead(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, error) {
	resultingHead, err := registry.inspectRecoveredRebaseHead(ctx, request)
	if err != nil {
		return "", err
	}
	if err := registry.requireServerRebaseProof(ctx, repository, request, resultingHead); err != nil {
		return "", err
	}
	if err := registry.promoteCompletedRebaseProof(ctx, request, resultingHead); err != nil {
		return "", err
	}
	return resultingHead, nil
}

func (registry *Registry) completeServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	directory, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil || !found || proof.resultingHead != "" && proof.resultingHead != resultingHead {
		return errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	candidateCommits, err := registry.rebaseCommitRange(
		ctx, repository, request.Candidate.BaseRevision, request.Candidate.HeadRevision,
	)
	if err != nil || !sameRebaseCommits(proof.candidateCommits, candidateCommits) {
		return errors.New("apply integration candidate: server rebase range proof differs")
	}
	resultCommits, err := registry.rebaseCommitRange(ctx, repository, request.Target.ExpectedHead, resultingHead)
	if err != nil || !sameRebaseRangeLength(candidateCommits, resultCommits) {
		return errors.New("apply integration candidate: rebased result omits candidate commits")
	}
	if proof.resultingHead == resultingHead {
		return nil
	}
	proof.resultingHead = resultingHead
	encoded := encodeServerRebaseProof(proof)
	nextPath := path + ".next"
	if next, nextFound, readErr := readServerRebaseProof(nextPath); readErr != nil {
		return readErr
	} else if nextFound {
		if string(encodeServerRebaseProof(next)) != string(encoded) {
			return errors.New("apply integration candidate: pending server rebase proof differs")
		}
	} else if err := createServerRebaseProof(nextPath, encoded); err != nil {
		return err
	}
	if err := os.Rename(nextPath, path); err != nil {
		return errors.New("apply integration candidate: server rebase proof could not be completed")
	}
	return syncDirectory(directory)
}

func (registry *Registry) requireServerRebaseProof(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
	resultingHead string,
) error {
	_, path, err := serverRebaseProofPath(repository, request)
	if err != nil {
		return err
	}
	proof, found, err := readServerRebaseProof(path)
	if err != nil || !found || proof.resultingHead != resultingHead {
		return errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	candidateCommits, candidateErr := registry.rebaseCommitRange(
		ctx, repository, request.Candidate.BaseRevision, request.Candidate.HeadRevision,
	)
	resultCommits, resultErr := registry.rebaseCommitRange(
		ctx, repository, request.Target.ExpectedHead, resultingHead,
	)
	if candidateErr != nil || resultErr != nil ||
		!sameRebaseCommits(proof.candidateCommits, candidateCommits) ||
		!sameRebaseRangeLength(candidateCommits, resultCommits) {
		return errors.New("apply integration candidate: server rebase range proof differs")
	}
	return nil
}

func (registry *Registry) rebaseCommitRange(
	ctx context.Context,
	repository Repository,
	base string,
	head string,
) ([]string, error) {
	output, err := runGitBytes(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-list", "--reverse", base+".."+head)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil, errors.New("rebase range is empty")
	}
	if len(lines) > maximumRebaseProofCommits {
		return nil, errors.New("rebase range exceeds its bound")
	}
	for _, line := range lines {
		if !gitRevisionPattern.MatchString(line) {
			return nil, errors.New("rebase range contains an invalid revision")
		}
	}
	return lines, nil
}

func serverRebaseProofPath(
	repository Repository,
	request application.IntegrationAdapterRequest,
) (string, string, error) {
	digest := strings.TrimPrefix(integrationRebaseProofRef(request), "refs/heads/comis-integration-proof-")
	if len(digest) != 64 || strings.ContainsAny(digest, "/\\\x00\r\n\t ") {
		return "", "", errors.New("apply integration candidate: server rebase proof identity is invalid")
	}
	directory := filepath.Join(repository.WorktreeRoot, ".comis-integration-proofs")
	return directory, filepath.Join(directory, digest), nil
}

func ensureServerRebaseProofDirectory(worktreeRoot, directory string) error {
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return errors.New("apply integration candidate: server rebase proof directory could not be created")
		}
		if err := syncDirectory(worktreeRoot); err != nil {
			return err
		}
		info, err = os.Lstat(directory)
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("apply integration candidate: server rebase proof directory is invalid")
	}
	return nil
}

func createServerRebaseProof(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("apply integration candidate: server rebase proof could not be created")
	}
	written, writeErr := file.Write(contents)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil || written != len(contents) || syncErr != nil || closeErr != nil {
		return errors.New("apply integration candidate: server rebase proof could not be persisted")
	}
	return nil
}

func readServerRebaseProof(path string) (serverRebaseProof, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return serverRebaseProof{}, false, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() > 200000 {
		return serverRebaseProof{}, false, errors.New("apply integration candidate: server rebase proof is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return serverRebaseProof{}, false, errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, 200001))
	syncErr := file.Sync()
	closeErr := file.Close()
	if readErr != nil || syncErr != nil || closeErr != nil || len(contents) > 200000 {
		return serverRebaseProof{}, false, errors.New("apply integration candidate: server rebase proof is unavailable")
	}
	proof, err := decodeServerRebaseProof(contents)
	if err != nil {
		return serverRebaseProof{}, false, err
	}
	return proof, true, nil
}

func encodeServerRebaseProof(proof serverRebaseProof) []byte {
	var builder strings.Builder
	builder.WriteString("version 1\ncommits ")
	builder.WriteString(strconv.Itoa(len(proof.candidateCommits)))
	builder.WriteByte('\n')
	for _, commit := range proof.candidateCommits {
		builder.WriteString(commit)
		builder.WriteByte('\n')
	}
	builder.WriteString("result ")
	if proof.resultingHead == "" {
		builder.WriteByte('-')
	} else {
		builder.WriteString(proof.resultingHead)
	}
	builder.WriteByte('\n')
	return []byte(builder.String())
}

func decodeServerRebaseProof(contents []byte) (serverRebaseProof, error) {
	if len(contents) == 0 || contents[len(contents)-1] != '\n' {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	lines := strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n")
	if len(lines) < 3 || lines[0] != "version 1" || !strings.HasPrefix(lines[1], "commits ") {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	count, err := strconv.Atoi(strings.TrimPrefix(lines[1], "commits "))
	if err != nil || count < 1 || count > maximumRebaseProofCommits || len(lines) != count+3 {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	proof := serverRebaseProof{candidateCommits: append([]string(nil), lines[2:2+count]...)}
	for _, commit := range proof.candidateCommits {
		if !gitRevisionPattern.MatchString(commit) {
			return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
		}
	}
	result := strings.TrimPrefix(lines[len(lines)-1], "result ")
	if lines[len(lines)-1] != "result "+result || result == "" {
		return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
	}
	if result != "-" {
		if !gitRevisionPattern.MatchString(result) {
			return serverRebaseProof{}, errors.New("apply integration candidate: server rebase proof is malformed")
		}
		proof.resultingHead = result
	}
	return proof, nil
}

func sameRebaseCommits(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func sameRebaseRangeLength(candidate, result []string) bool {
	return len(candidate) == len(result)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return errors.New("apply integration candidate: server rebase proof directory is unavailable")
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("apply integration candidate: server rebase proof directory could not be persisted")
	}
	return nil
}
