package git_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func integrationReceiptRefForTest(outcome string, request application.IntegrationAdapterRequest) string {
	canonical, _ := json.Marshal(request)
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("refs/comis/integration/%s/%x", outcome, digest)
}

func integrationRebaseProofRefForTest(request application.IntegrationAdapterRequest) string {
	if request.RecoveryOperationID != "" {
		request.OperationID = request.RecoveryOperationID
		request.RecoveryOperationID = ""
	}
	canonical, _ := json.Marshal(request)
	digest := sha256.Sum256(canonical)
	return fmt.Sprintf("refs/heads/comis-integration-proof-%x", digest)
}

func writeServerRebaseProofForTest(
	t *testing.T,
	fixture integrationFixture,
	request application.IntegrationAdapterRequest,
	resultingHead string,
	resolvedCommits ...string,
) {
	t.Helper()
	commits := strings.Fields(integrationGitOutput(
		t, fixture, fixture.repository.primary, "rev-list", "--reverse",
		request.Candidate.BaseRevision+".."+request.Candidate.HeadRevision,
	))
	directory := filepath.Join(fixture.repository.worktreeRoot, ".comis-integration-proofs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	digest := strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/comis-integration-proof-")
	var proof strings.Builder
	resultCommits := []string(nil)
	if resultingHead != "" {
		resultCommits = strings.Fields(integrationGitOutput(
			t, fixture, fixture.repository.primary, "rev-list", "--reverse",
			request.Target.ExpectedHead+".."+resultingHead,
		))
	}
	proof.WriteString("version 4\noperation ")
	if request.RecoveryOperationID != "" {
		proof.WriteString(request.RecoveryOperationID)
	} else {
		proof.WriteString(request.OperationID)
	}
	proof.WriteString("\ncandidates ")
	proof.WriteString(fmt.Sprintf("%d", len(commits)))
	proof.WriteByte('\n')
	for _, commit := range commits {
		proof.WriteString(commit)
		proof.WriteByte('\n')
	}
	proof.WriteString("patches ")
	proof.WriteString(fmt.Sprintf("%d", len(commits)))
	proof.WriteByte('\n')
	for _, commit := range commits {
		proof.WriteString(integrationPatchIdentityForTest(t, fixture, commit))
		proof.WriteByte('\n')
	}
	proof.WriteString("resolved ")
	proof.WriteString(fmt.Sprintf("%d", len(resolvedCommits)))
	proof.WriteByte('\n')
	for _, commit := range resolvedCommits {
		proof.WriteString(commit)
		proof.WriteByte('\n')
	}
	proof.WriteString("conflicts ")
	proof.WriteString(fmt.Sprintf("%d", len(resolvedCommits)))
	proof.WriteByte('\n')
	for _, commit := range resolvedCommits {
		proof.WriteString(commit)
		proof.WriteByte(' ')
		proof.WriteString(strings.Repeat("0", 64))
		proof.WriteString(" 1\nZml4dHVyZS50eHQ\n")
	}
	proof.WriteString("results ")
	proof.WriteString(fmt.Sprintf("%d", len(resultCommits)))
	proof.WriteByte('\n')
	for _, commit := range resultCommits {
		proof.WriteString(commit)
		proof.WriteByte('\n')
	}
	proof.WriteString("result ")
	if resultingHead == "" {
		proof.WriteByte('-')
	} else {
		proof.WriteString(resultingHead)
	}
	proof.WriteByte('\n')
	if err := os.WriteFile(filepath.Join(directory, digest), []byte(proof.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func integrationPatchIdentityForTest(t *testing.T, fixture integrationFixture, revision string) string {
	t.Helper()
	show := exec.Command(fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"show", "--format=%H", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames", "--full-index", "--binary", revision)
	show.Env = gitTestEnvironment(nil)
	patch, err := show.Output()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"patch-id", "--verbatim")
	command.Env = gitTestEnvironment(nil)
	command.Stdin = bytes.NewReader(patch)
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "-"
	}
	if len(fields) != 2 || fields[1] != revision {
		t.Fatalf("patch identity for %q = %q", revision, output)
	}
	return fields[0]
}

func serverRebaseProofPathForTest(
	fixture integrationFixture,
	request application.IntegrationAdapterRequest,
) string {
	digest := strings.TrimPrefix(integrationRebaseProofRefForTest(request), "refs/heads/comis-integration-proof-")
	return filepath.Join(fixture.repository.worktreeRoot, ".comis-integration-proofs", digest)
}

func lockIntegrationReceiptForTest(t *testing.T, fixture integrationFixture, receipt string) {
	t.Helper()
	lockPath := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--git-path", receipt) + ".lock"
	if !filepath.IsAbs(lockPath) {
		lockPath = filepath.Join(fixture.target.CanonicalPath, lockPath)
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runIntegrationGitExpectFailure(t *testing.T, executable string, arguments ...string) {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Env = gitTestEnvironment(nil)
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("Git fixture command unexpectedly succeeded: %s", output)
	}
}
