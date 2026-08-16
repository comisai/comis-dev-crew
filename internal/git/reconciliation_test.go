package git_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

type reconciliationCandidatePromoter interface {
	PromoteReconciliationCandidate(context.Context, application.ReconciliationWorkspaceRequest) (application.WorkspaceSnapshot, error)
}

func TestRegistry_InspectsOnlyExactOperationBoundCleanCandidate(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-api")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-reconcile-candidate", "task-reconcile-candidate")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "candidate.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "candidate")

	var inspector application.ReconciliationWorkspaceInspector = registry
	reconciliationRequest := application.ReconciliationWorkspaceRequest{
		PreparationOperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
		BaseRevision: request.BaseRevision,
	}
	snapshot, err := inspector.InspectReconciliationCandidate(context.Background(), reconciliationRequest)
	if err != nil {
		t.Fatalf("InspectReconciliationCandidate() error = %v", err)
	}
	if snapshot.TaskHandle != request.TaskHandle || snapshot.RepositoryID != request.RepositoryID ||
		snapshot.WorktreePath != prepared.CanonicalPath || snapshot.Branch != prepared.Branch ||
		snapshot.HeadRevision == request.BaseRevision || snapshot.Cleanliness != application.WorkspaceClean {
		t.Fatalf("InspectReconciliationCandidate() = %#v", snapshot)
	}

	t.Run("dirty worktree", func(t *testing.T) {
		if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "dirty.txt"), []byte("dirty\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectReconciliationCandidate(context.Background(), reconciliationRequest); err == nil {
			t.Fatal("InspectReconciliationCandidate(dirty) error = nil")
		}
		if err := os.Remove(filepath.Join(prepared.CanonicalPath, "dirty.txt")); err != nil {
			t.Fatal(err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(*application.ReconciliationWorkspaceRequest)
	}{
		{name: "preparation operation differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.PreparationOperationID = "prepare-reconcile-other"
		}},
		{name: "task differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.TaskHandle = "task-reconcile-other"
		}},
		{name: "repository differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.RepositoryID = "other-product"
		}},
		{name: "worktree differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.WorktreePath += "-other"
		}},
		{name: "base differs", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.BaseRevision = snapshot.HeadRevision
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			altered := reconciliationRequest
			test.mutate(&altered)
			if _, err := inspector.InspectReconciliationCandidate(context.Background(), altered); err == nil {
				t.Fatal("InspectReconciliationCandidate(altered authority) error = nil")
			}
		})
	}

	baseRequest := lifecycleRequest(t, fixture, "prepare-reconcile-base", "task-reconcile-base")
	basePrepared, err := registry.PrepareWorktree(context.Background(), baseRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.InspectReconciliationCandidate(context.Background(), application.ReconciliationWorkspaceRequest{
		PreparationOperationID: baseRequest.OperationID, TaskHandle: baseRequest.TaskHandle,
		RepositoryID: baseRequest.RepositoryID, WorktreePath: basePrepared.CanonicalPath,
		BaseRevision: baseRequest.BaseRevision,
	}); err == nil {
		t.Fatal("InspectReconciliationCandidate(base head) error = nil")
	}
}

func TestRegistry_RefusesUnavailableOrDivergentReconciliationCandidate(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-reconciliation-refusal")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-reconciliation-refusal", "task-reconciliation-refusal")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath, "add", "candidate.txt")
	runGit(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "candidate")
	base := application.ReconciliationWorkspaceRequest{
		PreparationOperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
		BaseRevision: request.BaseRevision,
	}
	tests := []struct {
		name   string
		mutate func(*application.ReconciliationWorkspaceRequest)
	}{
		{name: "repository is unavailable", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.RepositoryID = "product-missing"
		}},
		{name: "request identity is invalid", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.BaseRevision = "not-a-revision"
		}},
		{name: "worktree is unavailable", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.WorktreePath = filepath.Join(filepath.Dir(prepared.CanonicalPath), "missing-worktree")
		}},
		{name: "pinned base is unavailable", mutate: func(value *application.ReconciliationWorkspaceRequest) {
			value.BaseRevision = strings.Repeat("f", 40)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			altered := base
			test.mutate(&altered)
			if _, err := registry.InspectReconciliationCandidate(context.Background(), altered); err == nil {
				t.Fatal("InspectReconciliationCandidate() error = nil")
			}
		})
	}
	if _, err := (*devgit.Registry)(nil).InspectReconciliationCandidate(context.Background(), base); err == nil {
		t.Fatal("InspectReconciliationCandidate(nil registry) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := registry.InspectReconciliationCandidate(cancelled, base); err == nil {
		t.Fatal("InspectReconciliationCandidate(cancelled) error = nil")
	}
}

func TestRegistry_PromotesOnlyVerifiedLeasePrivateCandidateIntoExactSharedBranch(t *testing.T) {
	fixture := newRepositoryFixture(t, "product-private-candidate")
	registry := newLifecycleRegistry(t, fixture)
	request := lifecycleRequest(t, fixture, "prepare-private-candidate", "task-private-candidate")
	prepared, err := registry.PrepareWorktree(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	private := createLeasePrivateCandidate(t, fixture, prepared)
	reconciliationRequest := application.ReconciliationWorkspaceRequest{
		PreparationOperationID: request.OperationID, TaskHandle: request.TaskHandle,
		RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
		BaseRevision: request.BaseRevision,
	}

	snapshot, err := registry.InspectReconciliationCandidate(context.Background(), reconciliationRequest)
	if err != nil {
		t.Fatalf("InspectReconciliationCandidate(private) error = %v", err)
	}
	if snapshot.HeadRevision != private.head || snapshot.Branch != prepared.Branch ||
		snapshot.Cleanliness != application.WorkspaceClean {
		t.Fatalf("InspectReconciliationCandidate(private) = %#v", snapshot)
	}
	promoter, ok := any(registry).(reconciliationCandidatePromoter)
	if !ok {
		t.Fatal("Registry does not implement lease-private candidate promotion")
	}
	promoted, err := promoter.PromoteReconciliationCandidate(context.Background(), reconciliationRequest)
	if err != nil {
		t.Fatalf("PromoteReconciliationCandidate() error = %v", err)
	}
	if promoted != snapshot {
		t.Fatalf("PromoteReconciliationCandidate() = %#v, want %#v", promoted, snapshot)
	}
	if sharedHead := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"rev-parse", "HEAD"); sharedHead != private.head {
		t.Fatalf("shared head = %q, want private candidate %q", sharedHead, private.head)
	}
	if status := gitOutputAllowEmpty(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"status", "--porcelain=v2", "--untracked-files=all"); status != "" {
		t.Fatalf("promoted worktree status = %q, want clean", status)
	}
	replayed, err := promoter.PromoteReconciliationCandidate(context.Background(), reconciliationRequest)
	if err != nil || replayed != promoted {
		t.Fatalf("PromoteReconciliationCandidate(replay) = %#v, %v", replayed, err)
	}
}

func TestRegistry_RefusesUnsafeLeasePrivateCandidateWithoutMovingSharedBranch(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, repositoryFixture, devgit.PreparedWorktree, leasePrivateCandidate)
	}{
		{name: "source record names another Git directory", mutate: func(t *testing.T, fixture repositoryFixture, prepared devgit.PreparedWorktree, private leasePrivateCandidate) {
			writeLeasePrivateSource(t, private.root, fixture.primary, private.gitDir)
		}},
		{name: "candidate has uncommitted content", mutate: func(t *testing.T, _ repositoryFixture, prepared devgit.PreparedWorktree, _ leasePrivateCandidate) {
			if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "uncommitted.txt"), []byte("preserve\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "private Git configuration can execute a command", mutate: func(t *testing.T, _ repositoryFixture, _ devgit.PreparedWorktree, private leasePrivateCandidate) {
			private.sentinel = filepath.Join(filepath.Dir(private.root), "upload-pack-ran")
			config, err := os.OpenFile(filepath.Join(private.common, "config"), os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := config.WriteString("[uploadpack]\n\tpackObjectsHook = touch " + private.sentinel + "\n"); err != nil {
				_ = config.Close()
				t.Fatal(err)
			}
			if err := config.Close(); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "private administration root is a symbolic link", mutate: func(t *testing.T, _ repositoryFixture, _ devgit.PreparedWorktree, private leasePrivateCandidate) {
			relocated := private.root + "-relocated"
			if err := os.Rename(private.root, relocated); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(relocated, private.root); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRepositoryFixture(t, "product-private-refusal")
			registry := newLifecycleRegistry(t, fixture)
			request := lifecycleRequest(t, fixture, "prepare-private-refusal", "task-private-refusal")
			prepared, err := registry.PrepareWorktree(context.Background(), request)
			if err != nil {
				t.Fatal(err)
			}
			private := createLeasePrivateCandidate(t, fixture, prepared)
			test.mutate(t, fixture, prepared, private)
			promoter, ok := any(registry).(reconciliationCandidatePromoter)
			if !ok {
				t.Fatal("Registry does not implement lease-private candidate promotion")
			}
			_, err = promoter.PromoteReconciliationCandidate(context.Background(), application.ReconciliationWorkspaceRequest{
				PreparationOperationID: request.OperationID, TaskHandle: request.TaskHandle,
				RepositoryID: request.RepositoryID, WorktreePath: prepared.CanonicalPath,
				BaseRevision: request.BaseRevision,
			})
			if err == nil {
				t.Fatal("PromoteReconciliationCandidate(unsafe private candidate) error = nil")
			}
			if sharedHead := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
				"rev-parse", "HEAD"); sharedHead != request.BaseRevision {
				t.Fatalf("shared head = %q, want preserved base %q", sharedHead, request.BaseRevision)
			}
			if private.sentinel != "" {
				if _, err := os.Lstat(private.sentinel); !os.IsNotExist(err) {
					t.Fatalf("worker-controlled Git command executed: %v", err)
				}
			}
		})
	}
}

type leasePrivateCandidate struct {
	root     string
	common   string
	worktree string
	gitDir   string
	head     string
	sentinel string
}

func createLeasePrivateCandidate(
	t *testing.T,
	fixture repositoryFixture,
	prepared devgit.PreparedWorktree,
) leasePrivateCandidate {
	t.Helper()
	gitDir := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"rev-parse", "--path-format=absolute", "--git-dir")
	commonDir := gitOutput(t, fixture.gitExecutable, "--no-optional-locks", "-C", prepared.CanonicalPath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	privateRoot := filepath.Join(gitDir, ".comis-terminal-git")
	privateCommon := filepath.Join(privateRoot, "common")
	privateWorktree := filepath.Join(privateRoot, "worktree")
	for _, directory := range []string{
		filepath.Join(privateCommon, "objects", "info"),
		filepath.Join(privateCommon, "refs", "heads", filepath.Dir(prepared.Branch)),
		filepath.Join(privateCommon, "info"), privateWorktree,
		filepath.Join(prepared.CanonicalPath, ".comis-terminal-git", "common"),
		filepath.Join(prepared.CanonicalPath, ".comis-terminal-git", "worktree"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	headText := "ref: refs/heads/" + prepared.Branch + "\n"
	writeTestFile(t, filepath.Join(privateCommon, "HEAD"), []byte(headText))
	writeTestFile(t, filepath.Join(privateWorktree, "HEAD"), []byte(headText))
	writeTestFile(t, filepath.Join(privateWorktree, "commondir"), []byte(filepath.Join(prepared.CanonicalPath, ".comis-terminal-git", "common")+"\n"))
	writeTestFile(t, filepath.Join(privateWorktree, "gitdir"), []byte(filepath.Join(prepared.CanonicalPath, ".git")+"\n"))
	sharedIndex, err := os.ReadFile(filepath.Join(gitDir, "index"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(privateWorktree, "index"), sharedIndex)
	writeTestFile(t, filepath.Join(privateCommon, "config"), []byte("[core]\n\trepositoryformatversion = 0\n\tfilemode = true\n\tbare = false\n\tlogallrefupdates = true\n"))
	quotedWorkspace, err := json.Marshal(prepared.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(privateCommon, "system-config"), []byte("[safe]\n\tdirectory = "+string(quotedWorkspace)+"\n"))
	writeTestFile(t, filepath.Join(privateCommon, "objects", "info", "alternates"), []byte(filepath.Join(commonDir, "objects")+"\n"))
	writeTestFile(t, filepath.Join(privateCommon, "info", "exclude"), []byte("/.comis-terminal-git/\n"))
	writeTestFile(t, filepath.Join(privateCommon, "refs", "heads", prepared.Branch), []byte(prepared.BaseRevision+"\n"))
	writeLeasePrivateSource(t, privateRoot, commonDir, gitDir)
	sharedExclude := filepath.Join(commonDir, "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(sharedExclude), 0o700); err != nil {
		t.Fatal(err)
	}
	config, err := os.OpenFile(sharedExclude, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := config.WriteString("/.comis-terminal-git/\n"); err != nil {
		_ = config.Close()
		t.Fatal(err)
	}
	if err := config.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prepared.CanonicalPath, "candidate.txt"), []byte("candidate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitEnvironment := []string{
		"GIT_DIR=" + privateCommon,
		"GIT_WORK_TREE=" + prepared.CanonicalPath,
		"GIT_INDEX_FILE=" + filepath.Join(privateWorktree, "index"),
	}
	gitOutputWithEnvironment(t, fixture.gitExecutable, gitEnvironment, "add", "candidate.txt")
	gitOutputWithEnvironment(t, fixture.gitExecutable, gitEnvironment,
		"-c", "user.name=DevCrew Fixture", "-c", "user.email=fixture@example.invalid",
		"commit", "-m", "private candidate")
	head := gitOutputWithEnvironment(t, fixture.gitExecutable, gitEnvironment, "rev-parse", "HEAD")
	if parent := gitOutputWithEnvironment(t, fixture.gitExecutable, gitEnvironment, "rev-parse", head+"^"); parent != prepared.BaseRevision {
		t.Fatalf("private candidate parent = %q, want base %q", parent, prepared.BaseRevision)
	}
	return leasePrivateCandidate{
		root: privateRoot, common: privateCommon, worktree: privateWorktree, gitDir: gitDir, head: head,
	}
}

func writeLeasePrivateSource(t *testing.T, root, commonDir, gitDir string) {
	t.Helper()
	encoded, err := json.Marshal(struct {
		SchemaVersion int    `json:"schemaVersion"`
		CommonDir     string `json:"commonDir"`
		GitDir        string `json:"gitDir"`
	}{SchemaVersion: 1, CommonDir: commonDir, GitDir: gitDir})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "source.json"), append(encoded, '\n'))
}

func writeTestFile(t *testing.T, path string, contents []byte) {
	t.Helper()
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
}

func gitOutputAllowEmpty(t *testing.T, executable string, arguments ...string) string {
	t.Helper()
	command := exec.Command(executable, arguments...)
	command.Env = gitTestEnvironment(nil)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Git fixture output command failed: %v: %s", err, output)
	}
	return strings.TrimSpace(string(output))
}
