package service

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
)

func TestInstalledRuntime_ComposesVerifiedRepositoryIdentitiesAndControl(t *testing.T) {
	root := shortTempDir(t)
	configuration := installedServiceConfig(t, root)
	configured, err := composeInstalledRuntime(context.Background(), configuration)
	if err != nil {
		t.Fatalf("composeInstalledRuntime() error = %v", err)
	}
	if configured.Repositories == nil || configured.RequestedWorkspaceRoot != configuration.RepositoryComposition.WorkspaceRoot {
		t.Fatalf("installed repository configuration = %#v", configured)
	}
	taskID, err := configured.TaskIDs()
	if err != nil || !strings.HasPrefix(taskID, "task-") || len(taskID) != len("task-")+24 {
		t.Fatalf("installed task identity = %q, %v", taskID, err)
	}
	nonce, err := configured.RegistrationNonces()
	if err != nil || !strings.HasPrefix(nonce, "registration-nonce-") || len(nonce) != len("registration-nonce-")+32 {
		t.Fatalf("installed registration nonce = %q, %v", nonce, err)
	}
	store, err := sqlite.Open(context.Background(), filepath.Join(root, "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: configured.Repositories, TaskIDs: configured.TaskIDs,
		RegistrationNonces:     configured.RegistrationNonces,
		RequestedWorkspaceRoot: configured.RequestedWorkspaceRoot,
		PreparationTTL:         configured.PreparationTTL, Clock: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	control, err := composeComisControl(configured, mutations)
	if err != nil || control == nil {
		t.Fatalf("composeComisControl() = %#v, %v", control, err)
	}
	if passthrough, err := composeComisControl(Config{}, nil); err != nil || passthrough != nil {
		t.Fatalf("composeComisControl(empty) = %#v, %v", passthrough, err)
	}
}

func TestRun_StartsAndJoinsInstalledCompositionWithoutPreparedWork(t *testing.T) {
	configuration := installedServiceConfig(t, shortTempDir(t))
	ready := make(chan struct{})
	configuration.Ready = func() { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, configuration) }()
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("Run(installed) before ready error = %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("Run(installed) did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run(installed) cancellation error = %v", err)
	}
}

func TestInstalledRuntime_RejectsPartialMixedAndUnverifiedConfiguration(t *testing.T) {
	partial := Config{RepositoryComposition: &RepositoryComposition{}}
	if _, err := composeInstalledRuntime(context.Background(), partial); err == nil {
		t.Fatal("composeInstalledRuntime(partial) error = nil")
	}
	mixed := Config{
		MCPSocketPath: "/private/mcp.sock", ServiceInstanceID: "service-instance-fixture",
		RepositoryComposition: &RepositoryComposition{}, ComisComposition: &ComisComposition{},
		FixtureComposition: &FixtureComposition{Decision: "bounded"}, Repositories: serviceRepositoryCatalog{},
	}
	if _, err := composeInstalledRuntime(context.Background(), mixed); err == nil {
		t.Fatal("composeInstalledRuntime(mixed) error = nil")
	}
	root := shortTempDir(t)
	configuration := installedServiceConfig(t, root)
	configuration.FixtureComposition.Decision = " altered whitespace "
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(untrimmed decision) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.RepositoryComposition.WorkspaceRoot = configuration.RepositoryComposition.PrimaryCheckout
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(unverified workspace) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.RepositoryComposition.GitExecutable = "relative-git"
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid Git) error = nil")
	}
}

func TestComisComposition_RequiresMutationsCredentialAndValidAuthority(t *testing.T) {
	root := shortTempDir(t)
	credentialFile := filepath.Join(root, "comis.credential")
	writeServiceCredential(t, credentialFile, "installed_service_bearer_0123456789abcdef\n", 0o600)
	configured := Config{ComisComposition: &ComisComposition{
		SocketPath: filepath.Join(root, "comis.sock"), CredentialFile: credentialFile,
		HandshakeOperationID: "installed-handshake-0001",
	}}
	if _, err := composeComisControl(configured, nil); err == nil {
		t.Fatal("composeComisControl(no mutations) error = nil")
	}
	configured.ServiceInstanceID = "bad identity"
	if _, err := composeComisControl(configured, serviceMutationStub{}); err == nil {
		t.Fatal("composeComisControl(invalid service identity) error = nil")
	}
	configured.ServiceInstanceID = "service-instance-fixture"
	configured.ComisComposition.CredentialFile = filepath.Join(root, "missing")
	if _, err := composeComisControl(configured, serviceMutationStub{}); err == nil {
		t.Fatal("composeComisControl(missing credential) error = nil")
	}
	configured.ComisComposition.CredentialFile = credentialFile
	configured.ComisComposition.HandshakeOperationID = "bad operation"
	if _, err := composeComisControl(configured, serviceMutationStub{}); err == nil {
		t.Fatal("composeComisControl(invalid handshake operation) error = nil")
	}
}

func TestComposeMutations_RejectsIncompleteMCPAndAuthorityConfiguration(t *testing.T) {
	store, err := sqlite.Open(context.Background(), filepath.Join(shortTempDir(t), "state", "devcrew.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	clock := func() time.Time { return time.Now().UTC() }
	if _, err := composeMutations(Config{MCPSocketPath: "/private/mcp.sock"}, store, clock); err == nil {
		t.Fatal("composeMutations(MCP only) error = nil")
	}
	configuration := Config{
		Repositories:       serviceRepositoryCatalog{},
		TaskIDs:            func() (string, error) { return "task-composition", nil },
		RegistrationNonces: func() (string, error) { return "registration-nonce_composition", nil },
		PreparationTTL:     time.Minute,
	}
	if _, err := composeMutations(configuration, store, clock); err == nil {
		t.Fatal("composeMutations(no service instance) error = nil")
	}
	configuration.ServiceInstanceID = "service-instance-composition"
	configuration.TaskIDs = nil
	if _, err := composeMutations(configuration, store, clock); err == nil {
		t.Fatal("composeMutations(missing task source) error = nil")
	}
}

func TestReadOwnerCredential_EnforcesCanonicalPrivateBoundedFile(t *testing.T) {
	root := shortTempDir(t)
	valid := filepath.Join(root, "valid.credential")
	writeServiceCredential(t, valid, "installed_service_bearer_0123456789abcdef\r\n", 0o600)
	credential, err := readOwnerCredential(valid)
	if err != nil || credential != "installed_service_bearer_0123456789abcdef" {
		t.Fatalf("readOwnerCredential(valid) = %q, %v", credential, err)
	}
	if _, err := readOwnerCredential("relative.credential"); err == nil {
		t.Fatal("readOwnerCredential(relative) error = nil")
	}
	if _, err := readOwnerCredential(filepath.Join(root, "missing")); err == nil {
		t.Fatal("readOwnerCredential(missing) error = nil")
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readOwnerCredential(directory); err == nil {
		t.Fatal("readOwnerCredential(directory) error = nil")
	}
	public := filepath.Join(root, "public.credential")
	writeServiceCredential(t, public, "installed_service_bearer_0123456789abcdef", 0o644)
	if _, err := readOwnerCredential(public); err == nil {
		t.Fatal("readOwnerCredential(public) error = nil")
	}
	oversized := filepath.Join(root, "oversized.credential")
	writeServiceCredential(t, oversized, strings.Repeat("a", 513), 0o600)
	if _, err := readOwnerCredential(oversized); err == nil {
		t.Fatal("readOwnerCredential(oversized) error = nil")
	}
	whitespace := filepath.Join(root, "whitespace.credential")
	writeServiceCredential(t, whitespace, "invalid credential value", 0o600)
	if _, err := readOwnerCredential(whitespace); err == nil {
		t.Fatal("readOwnerCredential(whitespace) error = nil")
	}
}

type serviceMutationStub struct{}

func (serviceMutationStub) ActivateManagedRun(context.Context, application.ActivateManagedRunCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func (serviceMutationStub) AbandonManagedRun(context.Context, application.AbandonManagedRunCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func installedServiceConfig(t *testing.T, root string) Config {
	t.Helper()
	gitExecutable, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	gitExecutable, err = filepath.Abs(gitExecutable)
	if err != nil {
		t.Fatal(err)
	}
	gitExecutable, err = filepath.EvalSymlinks(gitExecutable)
	if err != nil {
		t.Fatal(err)
	}
	approvedRoot := filepath.Join(root, "repositories")
	primary := filepath.Join(approvedRoot, "primary")
	worktreeRoot := filepath.Join(approvedRoot, "worktrees")
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(worktreeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	runServiceGit(t, primary, "init", "--initial-branch=main")
	runServiceGit(t, primary, "config", "user.name", "Service Fixture")
	runServiceGit(t, primary, "config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(primary, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runServiceGit(t, primary, "add", "README.md")
	runServiceGit(t, primary, "commit", "-m", "fixture")
	workspace := filepath.Join(worktreeRoot, "managed-run")
	runServiceGit(t, primary, "worktree", "add", "-b", "service-fixture", workspace)
	credentialFile := filepath.Join(root, "config", "comis.credential")
	writeServiceCredential(t, credentialFile, "installed_service_bearer_0123456789abcdef", 0o600)
	return Config{
		DatabasePath: filepath.Join(root, "state", "devcrew.db"), SocketPath: filepath.Join(root, "operator.sock"),
		MCPSocketPath: filepath.Join(root, "mcp.sock"), ServiceInstanceID: "service-instance-fixture",
		PreparationTTL: 10 * time.Minute,
		RepositoryComposition: &RepositoryComposition{
			GitExecutable: gitExecutable, ApprovedRoot: approvedRoot, RepositoryID: "product-api",
			PrimaryCheckout: primary, WorktreeRoot: worktreeRoot, DefaultBranch: "main", WorkspaceRoot: workspace,
		},
		ComisComposition: &ComisComposition{
			SocketPath: filepath.Join(root, "comis.sock"), CredentialFile: credentialFile,
			HandshakeOperationID: "installed-handshake-0001",
		},
		FixtureComposition: &FixtureComposition{Decision: "use the bounded fixture choice"},
	}
}

func writeServiceCredential(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func runServiceGit(t *testing.T, directory string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
}
