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
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
	"github.com/comisai/comis-dev-crew/internal/validation"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func TestInstalledRuntime_ComposesVerifiedRepositoryIdentitiesAndControl(t *testing.T) {
	root := shortTempDir(t)
	configuration := installedServiceConfig(t, root)
	configured, err := composeInstalledRuntime(context.Background(), configuration)
	if err != nil {
		t.Fatalf("composeInstalledRuntime() error = %v", err)
	}
	if configured.Repositories == nil || configured.Workspaces == nil {
		t.Fatalf("installed repository configuration = %#v", configured)
	}
	for _, shape := range []domain.TaskShape{domain.ShapeShip, domain.ShapeScout} {
		if err := configured.ValidationProfiles("required", shape); err != nil {
			t.Fatalf("ValidationProfiles(required, %s) error = %v", shape, err)
		}
	}
	if configured.candidateGit == nil || configured.workspaceInspector == nil || configured.reconciliationInspector == nil ||
		configured.validationCatalog == nil ||
		configured.pullRequests == nil || configured.cleanupRemover == nil || configured.cleanupForge == nil ||
		configured.validationMaxOutputBytes != 64<<10 ||
		configured.validationPollInterval != 25*time.Millisecond {
		t.Fatalf("installed candidate validation configuration = %#v", configured)
	}
	adapter, err := configured.WorkerHarnesses.ResolveWorkerHarness("codex-reviewed")
	if err != nil {
		t.Fatalf("ResolveWorkerHarness() error = %v", err)
	}
	if _, err := configured.WorkerHarnesses.ResolveWorkerHarness("codex-unreviewed"); err == nil {
		t.Fatal("ResolveWorkerHarness(unreviewed) error = nil")
	}
	claudeAdapter, err := configured.WorkerHarnesses.ResolveWorkerHarness("claude-reviewed")
	if err != nil || claudeAdapter.ID() != "claude" {
		t.Fatalf("ResolveWorkerHarness(Claude) = %#v, %v", claudeAdapter, err)
	}
	descriptor, err := adapter.BuildLaunchDescriptor(context.Background(), application.WorkerLaunchRequest{
		ProfileID: "codex-reviewed", Shape: domain.ShapeShip, WorkingDirectory: configuration.RepositoryComposition.PrimaryCheckout,
		TaskHandle: "task-installed-plan", ManagedRunID: "managed-run-installed-plan",
		WorkspaceLeaseID: "workspace-lease-installed-plan", BriefRevision: 1,
		BriefRevisionHash: strings.Repeat("a", 64),
		Attachment: application.RuntimeSocketAttachment{
			ExecutionAttachmentID: "execution-attachment-installed-plan",
			AttachmentTargetName:  "attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
			MountSocketPath:       "/run/comis/attachments/attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock",
			RelayIdentity:         strings.Repeat("ab", 32),
		},
	})
	if err != nil {
		t.Fatalf("BuildLaunchDescriptor(installed) error = %v", err)
	}
	if descriptor.ProfileID != "codex-reviewed" || descriptor.TerminalAllowEntry != "codex-confined" ||
		descriptor.Unattended || descriptor.DegradedReason != application.HarnessReasonLifecycleSignalUnknown {
		t.Fatalf("installed Codex launch posture = %#v", descriptor)
	}
	if len(descriptor.EnvironmentBindings) != 3 ||
		descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT"] != descriptor.Attachment.MountSocketPath ||
		descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT_TARGET_NAME"] != descriptor.Attachment.AttachmentTargetName ||
		descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT_IDENTITY"] != descriptor.Attachment.RelayIdentity {
		t.Fatalf("installed Codex attachment bindings = %#v", descriptor.EnvironmentBindings)
	}
	taskID, err := configured.TaskIDs("operation-installed-stable")
	if err != nil || !strings.HasPrefix(taskID, "task-") || len(taskID) != len("task-")+24 {
		t.Fatalf("installed task identity = %q, %v", taskID, err)
	}
	replayedTaskID, err := configured.TaskIDs("operation-installed-stable")
	if err != nil || replayedTaskID != taskID {
		t.Fatalf("replayed task identity = %q, %v, want %q", replayedTaskID, err, taskID)
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
		Store: store, Repositories: configured.Repositories, Workspaces: configured.Workspaces,
		WorkerProfiles: configured.WorkerProfiles, ValidationProfiles: configured.ValidationProfiles,
		RuntimeAttachments: serviceRuntimeAttachments{},
		TaskIDs:            configured.TaskIDs, RegistrationNonces: configured.RegistrationNonces,
		PreparationTTL: configured.PreparationTTL, Clock: func() time.Time { return time.Now().UTC() },
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

func TestInstalledControlReconnectBackoffPreservesMultipleHandshakeAttempts(t *testing.T) {
	if comisMaximumBackoff >= comisRequestTimeout/2 {
		t.Fatalf(
			"control reconnect maximum backoff = %s, want less than half the %s handshake window",
			comisMaximumBackoff,
			comisRequestTimeout,
		)
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
	case <-time.After(serviceReadyDeadline):
		t.Fatal("Run(installed) did not become ready")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run(installed) cancellation error = %v", err)
	}
}

func TestInstalledRuntime_ComposesFixtureBesideRealCandidatePipeline(t *testing.T) {
	configuration := installedServiceConfig(t, shortTempDir(t))
	configuration.FixtureComposition = &FixtureComposition{
		Decision: "use the bounded fixture choice", ArtifactRelativePath: "report.md",
	}
	configured, err := composeInstalledRuntime(context.Background(), configuration)
	if err != nil {
		t.Fatalf("composeInstalledRuntime(fixture) error = %v", err)
	}
	if configured.FixtureComposition == nil || configured.candidateGit == nil || configured.validationCatalog == nil ||
		configured.pullRequests == nil || configured.cleanupRemover == nil || configured.cleanupForge == nil {
		t.Fatalf("installed fixture candidate composition = %#v", configured)
	}
	for _, shape := range []domain.TaskShape{domain.ShapeShip, domain.ShapeScout} {
		if err := configured.WorkerProfiles("fixture-worker", shape); err != nil {
			t.Fatalf("WorkerProfiles(fixture-worker, %s) error = %v", shape, err)
		}
	}
}

func TestInstalledRuntime_ValidationProfilesRejectShapeIncompletePolicy(t *testing.T) {
	tests := []struct {
		name   string
		shape  domain.TaskShape
		mutate func(*validation.Profile)
	}{
		{name: "required local", shape: domain.ShapeShip, mutate: func(profile *validation.Profile) {
			profile.LocalChecks[0].Required = false
		}},
		{name: "required forge", shape: domain.ShapeShip, mutate: func(profile *validation.Profile) {
			profile.ForgeChecks[0].Required = false
		}},
		{name: "scout artifact", shape: domain.ShapeScout, mutate: func(profile *validation.Profile) {
			profile.ArtifactRules = nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			configuration := installedServiceConfig(t, shortTempDir(t))
			test.mutate(&configuration.ValidationComposition.Profiles[0])
			configured, err := composeInstalledRuntime(context.Background(), configuration)
			if err != nil {
				t.Fatal(err)
			}
			if err := configured.ValidationProfiles("required", test.shape); err == nil {
				t.Fatalf("ValidationProfiles(required, %s) error = nil", test.shape)
			}
		})
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
		CodexComposition: &CodexComposition{}, Repositories: serviceRepositoryCatalog{},
	}
	if _, err := composeInstalledRuntime(context.Background(), mixed); err == nil {
		t.Fatal("composeInstalledRuntime(mixed) error = nil")
	}
	root := shortTempDir(t)
	configuration := installedServiceConfig(t, root)
	configuration.CodexComposition.ExpectedVersion = "codex-cli 9.9.9"
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(version mismatch) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.CodexComposition.Network = workers.NetworkPosture("redirected")
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid profile) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.CodexComposition.ExpectedVersion = "unreviewed-version"
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid version pin) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.RepositoryComposition.DefaultBranch = "missing"
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(unverified default branch) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.RepositoryComposition.GitExecutable = "relative-git"
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid Git) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.ValidationComposition = nil
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(missing validation) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.ForgeComposition.PushCredentialFile = configuration.ForgeComposition.ReadCredentialFile
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(shared forge credential) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.ValidationComposition.MaxOutputBytes = 0
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid validation bound) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.ValidationComposition.Profiles = nil
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid validation catalog) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.ForgeComposition.ReadCredentialFile = filepath.Join(shortTempDir(t), "missing")
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(missing forge credential) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.ForgeComposition.APIBaseURL = "http://example.com"
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid forge route) error = nil")
	}
	configuration = installedServiceConfig(t, shortTempDir(t))
	configuration.FixtureComposition = &FixtureComposition{Decision: " ", ArtifactRelativePath: "report.md"}
	if _, err := composeInstalledRuntime(context.Background(), configuration); err == nil {
		t.Fatal("composeInstalledRuntime(invalid fixture) error = nil")
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
		Repositories: serviceRepositoryCatalog{}, Workspaces: serviceWorkspacePreparer{root: "/approved/worktrees/task-composition"},
		WorkerProfiles: func(string, domain.TaskShape) error { return nil }, ValidationProfiles: func(string, domain.TaskShape) error { return nil },
		RuntimeAttachments: serviceRuntimeAttachments{},
		TaskIDs:            func(string) (string, error) { return "task-composition", nil },
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
	writeServiceCredential(t, oversized, strings.Repeat("a", 4097), 0o600)
	if _, err := readOwnerCredential(oversized); err == nil {
		t.Fatal("readOwnerCredential(oversized) error = nil")
	}
	whitespace := filepath.Join(root, "whitespace.credential")
	writeServiceCredential(t, whitespace, "invalid credential value", 0o600)
	if _, err := readOwnerCredential(whitespace); err == nil {
		t.Fatal("readOwnerCredential(whitespace) error = nil")
	}
}

func TestReadOwnerCredential_AcceptsBoundedSSHDeployKeyMaterial(t *testing.T) {
	root := shortTempDir(t)
	path := filepath.Join(root, "push-key.credential")
	material := strings.Repeat("a", 1024)
	writeServiceCredential(t, path, material, 0o600)

	credential, err := readOwnerCredential(path)
	if err != nil || credential != material {
		t.Fatalf("readOwnerCredential(SSH deploy key) length = %d, %v", len(credential), err)
	}
}

type serviceMutationStub struct{}

func (serviceMutationStub) ActivateManagedRun(context.Context, application.ActivateManagedRunCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func (serviceMutationStub) AbandonManagedRun(context.Context, application.AbandonManagedRunCommand) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

func (serviceMutationStub) RecordTerminalEvent(context.Context, application.RecordTerminalEventCommand) (application.MutationResult, error) {
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
	credentialFile := filepath.Join(root, "config", "comis.credential")
	writeServiceCredential(t, credentialFile, "installed_service_bearer_0123456789abcdef", 0o600)
	readCredentialFile := filepath.Join(root, "config", "forge-read.credential")
	pushCredentialFile := filepath.Join(root, "config", "forge-push.credential")
	writeServiceCredential(t, readCredentialFile, "forge_read_bearer_0123456789abcdef", 0o600)
	writeServiceCredential(t, pushCredentialFile, "forge_push_bearer_0123456789abcdef", 0o600)
	credentialDirectory := filepath.Join(root, "forge-credentials")
	if err := os.MkdirAll(credentialDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	validationExecutable, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	validationExecutable, err = filepath.Abs(validationExecutable)
	if err != nil {
		t.Fatal(err)
	}
	validationExecutable, err = filepath.EvalSymlinks(validationExecutable)
	if err != nil {
		t.Fatal(err)
	}
	return Config{
		DatabasePath: filepath.Join(root, "state", "devcrew.db"), SocketPath: filepath.Join(root, "operator.sock"),
		MCPSocketPath: filepath.Join(root, "mcp.sock"), RuntimeRoot: filepath.Join(root, "runtime"), ServiceInstanceID: "service-instance-fixture",
		PreparationTTL: 10 * time.Minute,
		RepositoryComposition: &RepositoryComposition{
			GitExecutable: gitExecutable, ApprovedRoot: approvedRoot, RepositoryID: "product-api",
			PrimaryCheckout: primary, WorktreeRoot: worktreeRoot, DefaultBranch: "main",
		},
		ComisComposition: &ComisComposition{
			SocketPath: filepath.Join(root, "comis.sock"), CredentialFile: credentialFile,
			HandshakeOperationID: "installed-handshake-0001",
		},
		CodexComposition: &CodexComposition{
			ProfileID: "codex-reviewed", Executable: serviceCodexExecutable(t, root),
			ExpectedVersion: "codex-cli 0.147.0", Model: "gpt-5.5-codex", Effort: "high",
			TerminalAllowEntryID: "codex-confined", Network: workers.NetworkRestricted, ConcurrencyLimit: 2,
		},
		ClaudeComposition: &ClaudeComposition{
			ProfileID: "claude-reviewed", Executable: serviceClaudeExecutable(t, root),
			ExpectedVersion: "2.1.224 (Claude Code)", Model: "claude-opus-4-6", Effort: "high",
			TerminalAllowEntryID: "claude-confined", Network: workers.NetworkRestricted, ConcurrencyLimit: 2,
			ConfigDirectory: serviceClaudeConfigDirectory(t, root),
		},
		ValidationComposition: &ValidationComposition{
			Programs: []validation.Program{{ID: "repo-check", Executable: validationExecutable}},
			Profiles: []validation.Profile{{
				ID: "required", EvidenceTTL: 10 * time.Minute,
				LocalChecks: []validation.LocalCheck{{
					ID: "unit", ProgramID: "repo-check", Required: true, Timeout: time.Minute,
					Arguments: []validation.ArgumentTemplate{{Kind: validation.ArgumentLiteral, Value: "--version"}},
				}},
				ForgeChecks:   []validation.ForgeCheck{{Name: "ci/unit", Required: true}},
				ArtifactRules: []validation.ArtifactRule{{Kind: validation.ArtifactRegularFile, RelativePath: "report.md", MediaType: "text/markdown", MaxBytes: 1 << 20}},
			}},
			MaxOutputBytes: 64 << 10, PollInterval: 25 * time.Millisecond,
		},
		ForgeComposition: &ForgeComposition{
			APIBaseURL: "http://127.0.0.1:1", Owner: "fixture-owner", Repository: "fixture-repository",
			RemoteURL:          "file://" + filepath.Join(root, "forge", "fixture.git"),
			ReadCredentialFile: readCredentialFile, PushCredentialFile: pushCredentialFile,
			CredentialDirectory: credentialDirectory, LocalFixtureRemoteRoot: root,
		},
	}
}

func serviceCodexExecutable(t *testing.T, root string) string {
	t.Helper()
	executable := filepath.Join(root, "bin", "codex")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'codex-cli 0.147.0\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func serviceClaudeExecutable(t *testing.T, root string) string {
	t.Helper()
	executable := filepath.Join(root, "bin", "claude")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '2.1.224 (Claude Code)\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func serviceClaudeConfigDirectory(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "claude-config")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
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

func TestRandomIdentity_WhenEntropyIsRequested_ReturnsAPrefixedHexIdentity(t *testing.T) {
	// Identities must be distinct per call: a repeated one would let two
	// runtime registrations collide on the same durable key.
	first, err := randomIdentity("attachment", 16)
	if err != nil {
		t.Fatalf("randomIdentity error = %v", err)
	}
	second, err := randomIdentity("attachment", 16)
	if err != nil {
		t.Fatalf("randomIdentity error = %v", err)
	}
	if first == second {
		t.Error("two identities matched")
	}
	if len(first) != len("attachment-")+32 {
		t.Errorf("identity %q is not the expected prefixed hex width", first)
	}
	if !strings.HasPrefix(first, "attachment-") {
		t.Errorf("identity %q lost its prefix", first)
	}
}

// Cancellation joins the same durable mutation surface; the double accepts it.
func (serviceMutationStub) CancelManagedRun(
	_ context.Context,
	_ application.CancelManagedRunCommand,
) (application.MutationResult, error) {
	return application.MutationResult{}, nil
}

// serviceReadyDeadline bounds how long a test waits for the service to finish
// starting.
//
// It is deliberately far longer than startup takes. The assertion is that
// startup completes at all — a service that fails to become ready never becomes
// ready, so a generous bound loses nothing. A tight one instead measures how
// busy the machine is: these waits passed alone and failed inside the full
// suite, reporting scheduler contention as a product failure and training the
// reader to re-run rather than read the result.
const serviceReadyDeadline = 60 * time.Second
