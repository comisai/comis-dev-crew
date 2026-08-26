package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const maximumRebaseSequencerBytes = 1024 * 1024

func (registry *Registry) validateRebaseSequencerAuthority(
	ctx context.Context,
	repository Repository,
	request application.IntegrationAdapterRequest,
) error {
	original := originalIntegrationRequest(request)
	proof, found, err := registry.serverRebaseProof(repository, original)
	if err != nil || !found || proof.operationID != original.OperationID || proof.resultingHead != "" ||
		len(proof.candidateCommits) == 0 {
		return errors.New("apply integration candidate: rebase sequencer proof is unavailable")
	}
	gitDirectory, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return errors.New("apply integration candidate: rebase sequencer directory is unavailable")
	}
	root, err := os.OpenRoot(gitDirectory)
	if err != nil {
		return errors.New("apply integration candidate: rebase sequencer directory is unavailable")
	}
	defer root.Close()
	sequencer, err := root.OpenRoot("rebase-merge")
	if err != nil {
		return errors.New("apply integration candidate: rebase sequencer is unavailable")
	}
	defer sequencer.Close()
	done, err := readRebaseSequence(sequencer, "done")
	if err != nil || len(done) == 0 || len(done) > len(proof.candidateCommits) {
		return errors.New("apply integration candidate: completed rebase sequence differs")
	}
	remaining, err := readRebaseSequence(sequencer, "git-rebase-todo")
	if err != nil || len(done)+len(remaining) != len(proof.candidateCommits) {
		return errors.New("apply integration candidate: remaining rebase sequence differs")
	}
	sequence := append(append([]string(nil), done...), remaining...)
	for index, revision := range sequence {
		if !exactOrUniqueRevisionPrefix(revision, proof.candidateCommits[index], proof.candidateCommits) {
			return errors.New("apply integration candidate: rebase candidate order differs")
		}
	}
	rebaseHead, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C",
		request.Target.WorktreePath, "rev-parse", "--verify", "REBASE_HEAD^{commit}")
	if err != nil || rebaseHead != proof.candidateCommits[len(done)-1] {
		return errors.New("apply integration candidate: rebase stopped commit differs")
	}
	checks := []struct {
		name string
		want string
	}{
		{"onto", request.Target.ExpectedHead},
		{"orig-head", request.Candidate.HeadRevision},
		{"head-name", integrationRebaseProofRef(original)},
		{"stopped-sha", rebaseHead},
		{"msgnum", strconv.Itoa(len(done))},
		{"end", strconv.Itoa(len(proof.candidateCommits))},
	}
	for _, check := range checks {
		value, readErr := readRebaseSequencerValue(sequencer, check.name)
		if readErr != nil || value != check.want {
			return fmt.Errorf("apply integration candidate: rebase sequencer %s differs", check.name)
		}
	}
	return nil
}

func readRebaseSequence(root *os.Root, name string) ([]string, error) {
	contents, err := readRebaseSequencerFile(root, name)
	if err != nil {
		return nil, err
	}
	var revisions []string
	for _, line := range strings.Split(strings.TrimSuffix(string(contents), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "pick" || !lowerHex(fields[1]) || len(fields[1]) < 7 || len(fields[1]) > 64 {
			return nil, errors.New("rebase sequencer contains an unsupported directive")
		}
		revisions = append(revisions, fields[1])
	}
	return revisions, nil
}

func readRebaseSequencerValue(root *os.Root, name string) (string, error) {
	contents, err := readRebaseSequencerFile(root, name)
	if err != nil {
		return "", err
	}
	value := strings.TrimSuffix(string(contents), "\n")
	if value == "" || strings.ContainsAny(value, "\x00\r\n\t") {
		return "", errors.New("rebase sequencer value is invalid")
	}
	return value, nil
}

func readRebaseSequencerFile(root *os.Root, name string) ([]byte, error) {
	identity, err := root.Lstat(name)
	if err != nil || !identity.Mode().IsRegular() || identity.Mode()&os.ModeSymlink != 0 ||
		identity.Size() > maximumRebaseSequencerBytes {
		return nil, errors.New("rebase sequencer file is invalid")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, errors.New("rebase sequencer file is unavailable")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(identity, opened) {
		_ = file.Close()
		return nil, errors.New("rebase sequencer file identity changed")
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximumRebaseSequencerBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(contents) > maximumRebaseSequencerBytes {
		return nil, errors.New("rebase sequencer file could not be read")
	}
	return contents, nil
}

func exactOrUniqueRevisionPrefix(revision, want string, candidates []string) bool {
	if revision == want {
		return true
	}
	if len(revision) >= len(want) || !strings.HasPrefix(want, revision) {
		return false
	}
	matches := 0
	for _, candidate := range candidates {
		if strings.HasPrefix(candidate, revision) {
			matches++
		}
	}
	return matches == 1
}
