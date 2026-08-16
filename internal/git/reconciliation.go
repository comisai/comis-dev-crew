package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/comisai/comis-dev-crew/internal/application"
)

const (
	leasePrivateGitDirectory = ".comis-terminal-git"
	leasePrivateSourceRecord = "source.json"
	maximumGitControlBytes   = 16 * 1024 * 1024
)

type leasePrivateSource struct {
	SchemaVersion int    `json:"schemaVersion"`
	CommonDir     string `json:"commonDir"`
	GitDir        string `json:"gitDir"`
}

type leasePrivateCandidate struct {
	commonDir string
	worktree  string
	snapshot  CandidateSnapshot
}

type reconciliationCandidateEvidence struct {
	repository     Repository
	expectedBranch string
	shared         CandidateSnapshot
	private        *leasePrivateCandidate
	snapshot       application.WorkspaceSnapshot
}

// InspectReconciliationCandidate proves that the exact operation-bound task
// branch contains one clean candidate descended from, but different to, its
// pinned base. Lease-private candidate recognition is read-only.
func (registry *Registry) InspectReconciliationCandidate(
	ctx context.Context,
	request application.ReconciliationWorkspaceRequest,
) (application.WorkspaceSnapshot, error) {
	if registry == nil || ctx == nil {
		return application.WorkspaceSnapshot{}, errors.New("inspect reconciliation candidate: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return application.WorkspaceSnapshot{}, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	evidence, err := registry.inspectReconciliationCandidate(ctx, request)
	if err != nil {
		return application.WorkspaceSnapshot{}, err
	}
	return evidence.snapshot, nil
}

// PromoteReconciliationCandidate performs the explicit host-side handoff from
// verified lease-private Git administration into the exact prepared branch.
// It updates only the branch reference and worktree index; workspace files are
// never replaced.
func (registry *Registry) PromoteReconciliationCandidate(
	ctx context.Context,
	request application.ReconciliationWorkspaceRequest,
) (application.WorkspaceSnapshot, error) {
	if registry == nil || ctx == nil {
		return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: registry and context are required")
	}
	if err := ctx.Err(); err != nil {
		return application.WorkspaceSnapshot{}, err
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	evidence, err := registry.inspectReconciliationCandidate(ctx, request)
	if err != nil {
		return application.WorkspaceSnapshot{}, err
	}
	if evidence.private == nil {
		return evidence.snapshot, nil
	}
	privateHead := evidence.private.snapshot.HeadRevision
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "-c", "gc.auto=0", "--no-optional-locks",
		"-C", evidence.repository.PrimaryCheckout, "fetch", "--no-tags", "--no-write-fetch-head",
		"--no-recurse-submodules", evidence.private.commonDir, "refs/heads/"+evidence.expectedBranch); err != nil {
		return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: private objects could not be imported")
	}
	rechecked, err := registry.inspectReconciliationCandidate(ctx, request)
	if err != nil || rechecked.private == nil || rechecked.private.snapshot.HeadRevision != privateHead {
		return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: private candidate changed during handoff")
	}
	sharedHead := rechecked.shared.HeadRevision
	if sharedHead != request.BaseRevision && sharedHead != privateHead {
		return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: shared branch changed during handoff")
	}
	if sharedHead == request.BaseRevision {
		if _, err := runGitBytes(ctx, registry.gitExecutable,
			"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", evidence.repository.PrimaryCheckout,
			"update-ref", "refs/heads/"+evidence.expectedBranch, privateHead, request.BaseRevision); err != nil {
			return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: exact branch update was refused")
		}
	}
	if _, err := runGitBytes(ctx, registry.gitExecutable,
		"-c", "core.hooksPath=/dev/null", "--no-optional-locks", "-C", request.WorktreePath,
		"read-tree", privateHead); err != nil {
		return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: worktree index could not be synchronized")
	}
	final, err := registry.inspectSharedReconciliationCandidate(ctx, request, evidence.expectedBranch)
	if err != nil || final.HeadRevision != privateHead {
		return application.WorkspaceSnapshot{}, errors.New("promote reconciliation candidate: promoted workspace could not be verified")
	}
	return workspaceSnapshot(request.TaskHandle, final), nil
}

func (registry *Registry) inspectReconciliationCandidate(
	ctx context.Context,
	request application.ReconciliationWorkspaceRequest,
) (reconciliationCandidateEvidence, error) {
	if !repositoryIDPattern.MatchString(request.PreparationOperationID) ||
		!repositoryIDPattern.MatchString(request.TaskHandle) ||
		!repositoryIDPattern.MatchString(request.RepositoryID) ||
		!gitRevisionPattern.MatchString(request.BaseRevision) {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: request identity is invalid")
	}
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: repository is unavailable")
	}
	expectedBranch, operationSuffix := preparedBranch(
		request.RepositoryID, request.TaskHandle, request.PreparationOperationID,
	)
	if err := registry.validateOperationBranch(ctx, repository, expectedBranch, operationSuffix); err != nil {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: preparation branch is ambiguous")
	}
	resolvedBase, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", repository.PrimaryCheckout,
		"rev-parse", "--verify", request.BaseRevision+"^{commit}")
	if err != nil || resolvedBase != request.BaseRevision {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: pinned base is unavailable")
	}
	shared, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: request.WorktreePath,
	})
	if err != nil {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: worktree identity is unavailable")
	}
	if shared.Branch != expectedBranch {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: branch differs from preparation")
	}
	if shared.Cleanliness == CandidateClean && shared.HeadRevision != request.BaseRevision {
		if err := registry.verifySharedCandidateAncestry(ctx, repository, request.BaseRevision, shared.HeadRevision); err != nil {
			return reconciliationCandidateEvidence{}, err
		}
		return reconciliationCandidateEvidence{
			repository: repository, expectedBranch: expectedBranch, shared: shared,
			snapshot: workspaceSnapshot(request.TaskHandle, shared),
		}, nil
	}
	private, err := registry.inspectLeasePrivateCandidate(ctx, repository, request, expectedBranch)
	if err != nil {
		if shared.HeadRevision == request.BaseRevision && shared.Cleanliness == CandidateClean {
			return reconciliationCandidateEvidence{}, fmt.Errorf("inspect reconciliation candidate: head matches pinned base: %w", err)
		}
		return reconciliationCandidateEvidence{}, fmt.Errorf("inspect reconciliation candidate: worktree is not clean: %w", err)
	}
	if shared.HeadRevision != request.BaseRevision && shared.HeadRevision != private.snapshot.HeadRevision {
		return reconciliationCandidateEvidence{}, errors.New("inspect reconciliation candidate: shared and private heads differ")
	}
	return reconciliationCandidateEvidence{
		repository: repository, expectedBranch: expectedBranch, shared: shared, private: &private,
		snapshot: workspaceSnapshot(request.TaskHandle, private.snapshot),
	}, nil
}

func (registry *Registry) inspectSharedReconciliationCandidate(
	ctx context.Context,
	request application.ReconciliationWorkspaceRequest,
	expectedBranch string,
) (CandidateSnapshot, error) {
	snapshot, err := registry.InspectCandidate(ctx, CandidateSnapshotRequest{
		TaskHandle: request.TaskHandle, RepositoryID: request.RepositoryID,
		WorktreePath: request.WorktreePath,
	})
	if err != nil || snapshot.Branch != expectedBranch || snapshot.Cleanliness != CandidateClean ||
		snapshot.HeadRevision == request.BaseRevision {
		return CandidateSnapshot{}, errors.New("inspect promoted reconciliation candidate: workspace differs")
	}
	repository, err := registry.Resolve(request.RepositoryID)
	if err != nil || registry.verifySharedCandidateAncestry(ctx, repository, request.BaseRevision, snapshot.HeadRevision) != nil {
		return CandidateSnapshot{}, errors.New("inspect promoted reconciliation candidate: ancestry differs")
	}
	return snapshot, nil
}

func (registry *Registry) verifySharedCandidateAncestry(
	ctx context.Context,
	repository Repository,
	baseRevision string,
	headRevision string,
) error {
	descendsFromBase, err := gitPredicate(ctx, registry.gitExecutable, "--no-optional-locks",
		"-C", repository.PrimaryCheckout, "merge-base", "--is-ancestor", baseRevision, headRevision)
	if err != nil || !descendsFromBase {
		return errors.New("inspect reconciliation candidate: head diverges from pinned base")
	}
	return nil
}

func (registry *Registry) inspectLeasePrivateCandidate(
	ctx context.Context,
	repository Repository,
	request application.ReconciliationWorkspaceRequest,
	expectedBranch string,
) (leasePrivateCandidate, error) {
	gitDir, err := runGit(ctx, registry.gitExecutable, "--no-optional-locks", "-C", request.WorktreePath,
		"rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil || validateCanonicalDirectory(gitDir) != nil ||
		!pathWithin(filepath.Join(repository.GitCommonDir, "worktrees"), gitDir, true) {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: Git directory is unavailable")
	}
	privateRoot := filepath.Join(gitDir, leasePrivateGitDirectory)
	privateCommon := filepath.Join(privateRoot, "common")
	privateWorktree := filepath.Join(privateRoot, "worktree")
	for _, directory := range []string{privateRoot, privateCommon, privateWorktree} {
		if validateCanonicalDirectory(directory) != nil {
			return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: administration is unavailable")
		}
	}
	sourceBytes, err := readGitControlFile(filepath.Join(privateRoot, leasePrivateSourceRecord), 4_096)
	if err != nil {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: source identity is unavailable")
	}
	var source leasePrivateSource
	decoder := json.NewDecoder(bytes.NewReader(sourceBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&source) != nil || source.SchemaVersion != 1 || source.CommonDir != repository.GitCommonDir ||
		source.GitDir != gitDir || decoder.Decode(&struct{}{}) != io.EOF {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: source identity differs")
	}
	if err := validateLeasePrivateControlFiles(repository, request, gitDir, privateCommon, privateWorktree, expectedBranch); err != nil {
		return leasePrivateCandidate{}, err
	}
	environment := gitWorkspaceEnvironment{
		gitDir: privateCommon, gitWorkTree: request.WorktreePath,
		gitIndex: filepath.Join(privateWorktree, "index"),
	}
	branch, err := runGitInWorkspace(ctx, registry.gitExecutable, environment,
		"-c", "core.hooksPath=/dev/null", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch != expectedBranch {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: branch identity differs")
	}
	head, err := runGitInWorkspace(ctx, registry.gitExecutable, environment,
		"-c", "core.hooksPath=/dev/null", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || !gitRevisionPattern.MatchString(head) || head == request.BaseRevision {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: head is unavailable")
	}
	status, err := runGitBytesInWorkspace(ctx, registry.gitExecutable, environment,
		"-c", "core.hooksPath=/dev/null", "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil || len(status) != 0 {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: worktree is not clean")
	}
	descendsFromBase, err := gitPredicateInWorkspace(ctx, registry.gitExecutable, environment,
		"-c", "core.hooksPath=/dev/null", "merge-base", "--is-ancestor", request.BaseRevision, head)
	if err != nil || !descendsFromBase {
		return leasePrivateCandidate{}, errors.New("inspect lease-private candidate: head diverges from pinned base")
	}
	return leasePrivateCandidate{
		commonDir: privateCommon, worktree: privateWorktree,
		snapshot: CandidateSnapshot{
			RepositoryID: request.RepositoryID, WorktreePath: request.WorktreePath,
			Branch: expectedBranch, HeadRevision: head, Cleanliness: CandidateClean,
		},
	}, nil
}

func validateLeasePrivateControlFiles(
	repository Repository,
	request application.ReconciliationWorkspaceRequest,
	gitDir string,
	privateCommon string,
	privateWorktree string,
	expectedBranch string,
) error {
	headText := "ref: refs/heads/" + expectedBranch + "\n"
	quotedWorkspace, err := json.Marshal(request.WorktreePath)
	if err != nil {
		return errors.New("inspect lease-private candidate: workspace identity cannot be encoded")
	}
	hasWorktreeConfig, err := regularFileExists(filepath.Join(gitDir, "config.worktree"))
	if err != nil {
		return errors.New("inspect lease-private candidate: shared worktree configuration is unavailable")
	}
	repositoryVersion := "0"
	objectFormat := ""
	if len(request.BaseRevision) == 64 {
		repositoryVersion = "1"
		objectFormat = "\n[extensions]\n\tobjectFormat = sha256"
	}
	worktreeConfig := ""
	if hasWorktreeConfig {
		if objectFormat == "" {
			worktreeConfig = "\n[extensions]"
		}
		worktreeConfig += "\n\tworktreeConfig = true"
	}
	expectedConfig := fmt.Sprintf("[core]\n\trepositoryformatversion = %s\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true%s%s\n", repositoryVersion, objectFormat, worktreeConfig)
	expected := []struct {
		path     string
		contents string
	}{
		{filepath.Join(privateCommon, "HEAD"), headText},
		{filepath.Join(privateWorktree, "HEAD"), headText},
		{filepath.Join(privateWorktree, "commondir"), filepath.Join(request.WorktreePath, leasePrivateGitDirectory, "common") + "\n"},
		{filepath.Join(privateWorktree, "gitdir"), filepath.Join(request.WorktreePath, ".git") + "\n"},
		{filepath.Join(privateCommon, "system-config"), "[safe]\n\tdirectory = " + string(quotedWorkspace) + "\n"},
		{filepath.Join(privateCommon, "objects", "info", "alternates"), filepath.Join(repository.GitCommonDir, "objects") + "\n"},
		{filepath.Join(privateCommon, "info", "exclude"), "/" + leasePrivateGitDirectory + "/\n"},
	}
	for _, file := range expected {
		contents, err := readGitControlFile(file.path, maximumGitControlBytes)
		if err != nil || string(contents) != file.contents {
			return errors.New("inspect lease-private candidate: control files differ")
		}
	}
	if err := validateLeasePrivateConfig(filepath.Join(privateCommon, "config"), expectedConfig); err != nil {
		return err
	}
	if _, err := readGitControlFile(filepath.Join(privateWorktree, "index"), maximumGitControlBytes); err != nil {
		return errors.New("inspect lease-private candidate: index is unavailable")
	}
	if _, err := readGitControlFile(filepath.Join(privateCommon, "refs", "heads", expectedBranch), 4_096); err != nil {
		return errors.New("inspect lease-private candidate: branch reference is unavailable")
	}
	for _, pair := range [][2]string{
		{filepath.Join(gitDir, "config.worktree"), filepath.Join(privateWorktree, "config.worktree")},
		{filepath.Join(gitDir, "info", "sparse-checkout"), filepath.Join(privateWorktree, "info", "sparse-checkout")},
		{filepath.Join(repository.GitCommonDir, "shallow"), filepath.Join(privateCommon, "shallow")},
	} {
		if err := compareOptionalGitControlFile(pair[0], pair[1]); err != nil {
			return errors.New("inspect lease-private candidate: copied control file differs")
		}
	}
	for _, forbidden := range []string{
		filepath.Join(privateCommon, "objects", "info", "http-alternates"),
		filepath.Join(privateCommon, "info", "grafts"),
	} {
		if exists, err := regularFileExists(forbidden); err != nil || exists {
			return errors.New("inspect lease-private candidate: unsupported object indirection exists")
		}
	}
	return nil
}

func validateLeasePrivateConfig(path string, expectedCore string) error {
	contents, err := readGitControlFile(path, maximumGitControlBytes)
	if err != nil {
		return errors.New("inspect lease-private candidate: configuration is unavailable")
	}
	configuration := string(contents)
	if configuration == expectedCore {
		return nil
	}
	if !strings.HasPrefix(configuration, expectedCore+"[user]\n") || !strings.HasSuffix(configuration, "\n") {
		return errors.New("inspect lease-private candidate: configuration contains unsupported keys")
	}
	lines := strings.Split(strings.TrimSuffix(strings.TrimPrefix(configuration, expectedCore), "\n"), "\n")
	if len(lines) < 2 || len(lines) > 3 || lines[0] != "[user]" {
		return errors.New("inspect lease-private candidate: user configuration is malformed")
	}
	seen := make(map[string]struct{}, 2)
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "\t") {
			return errors.New("inspect lease-private candidate: user configuration is malformed")
		}
		key, value, found := strings.Cut(line, " = ")
		key = strings.TrimPrefix(key, "\t")
		if !found || (key != "name" && key != "email") || value == "" || len([]byte(value)) > 512 ||
			strings.IndexFunc(value, unicode.IsControl) >= 0 {
			return errors.New("inspect lease-private candidate: user configuration is malformed")
		}
		if _, duplicate := seen[key]; duplicate {
			return errors.New("inspect lease-private candidate: user configuration is duplicated")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func readGitControlFile(path string, maximumBytes int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maximumBytes {
		return nil, errors.New("git control file is unavailable")
	}
	contents, err := os.ReadFile(path)
	if err != nil || int64(len(contents)) != info.Size() {
		return nil, errors.New("git control file could not be read")
	}
	return contents, nil
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, errors.New("git control path is unsafe")
	}
	return true, nil
}

func compareOptionalGitControlFile(source string, target string) error {
	sourceExists, err := regularFileExists(source)
	if err != nil {
		return err
	}
	targetExists, err := regularFileExists(target)
	if err != nil || sourceExists != targetExists {
		return errors.New("optional Git control file posture differs")
	}
	if !sourceExists {
		return nil
	}
	sourceContents, err := readGitControlFile(source, maximumGitControlBytes)
	if err != nil {
		return err
	}
	targetContents, err := readGitControlFile(target, maximumGitControlBytes)
	if err != nil || !bytes.Equal(sourceContents, targetContents) {
		return errors.New("optional Git control file contents differ")
	}
	return nil
}

func workspaceSnapshot(taskHandle string, snapshot CandidateSnapshot) application.WorkspaceSnapshot {
	return application.WorkspaceSnapshot{
		TaskHandle: taskHandle, RepositoryID: snapshot.RepositoryID,
		WorktreePath: snapshot.WorktreePath, Branch: snapshot.Branch,
		HeadRevision: snapshot.HeadRevision, Cleanliness: application.WorkspaceClean,
	}
}

var _ application.ReconciliationWorkspaceManager = (*Registry)(nil)
