package livecampaign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fixedOutputExecutor struct {
	output []byte
	seen   []Command
}

func (executor *fixedOutputExecutor) Run(_ context.Context, command Command) ([]byte, error) {
	executor.seen = append(executor.seen, command)
	return append([]byte(nil), executor.output...), nil
}

func TestRealExecutorUsesFixedArgvAndBoundedStdout(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executor := RealExecutor{}
	output, err := executor.Run(context.Background(), Command{
		Path: executable,
		Args: []string{"-test.run=TestLiveCampaignExecutorHelperProcess", "--", "print"},
		Env: map[string]string{
			"GO_WANT_LIVE_CAMPAIGN_HELPER": "1", "LIVE_CAMPAIGN_TEST_VALUE": "bounded",
		},
	})
	if err != nil || string(output) != "argv=print env=bounded" {
		t.Fatalf("Run(print) = %q, %v", output, err)
	}
	_, err = executor.Run(context.Background(), Command{
		Path: executable,
		Args: []string{"-test.run=TestLiveCampaignExecutorHelperProcess", "--", "oversized"},
		Env:  map[string]string{"GO_WANT_LIVE_CAMPAIGN_HELPER": "1"},
	})
	if err == nil || !strings.Contains(err.Error(), "output limit") {
		t.Fatalf("Run(oversized) error = %v", err)
	}
}

func TestRealExecutorDoesNotReturnChildStderr(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	_, err = (RealExecutor{}).Run(context.Background(), Command{
		Path: executable,
		Args: []string{"-test.run=TestLiveCampaignExecutorHelperProcess", "--", "fail"},
		Env:  map[string]string{"GO_WANT_LIVE_CAMPAIGN_HELPER": "1"},
	})
	if err == nil || strings.Contains(err.Error(), "sensitive-child-detail") {
		t.Fatalf("Run(fail) leaked child stderr: %v", err)
	}
}

func TestLiveCampaignExecutorHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_LIVE_CAMPAIGN_HELPER") != "1" {
		return
	}
	args := os.Args
	separator := -1
	for index, value := range args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator < 0 || separator+1 >= len(args) {
		os.Exit(2)
	}
	switch args[separator+1] {
	case "print":
		fmt.Printf("argv=print env=%s", os.Getenv("LIVE_CAMPAIGN_TEST_VALUE"))
	case "oversized":
		fmt.Print(strings.Repeat("x", maximumCommandOutputBytes+1))
	case "fail":
		fmt.Fprint(os.Stderr, "sensitive-child-detail")
		os.Exit(7)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestValidateRuntimeRejectsNonSocketAndNonPrivateDataRoot(t *testing.T) {
	manifest := validManifest()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(root, "program")
	if err := os.WriteFile(program, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest.DevCrew.CLIPath = program
	manifest.Comis.NodePath = program
	manifest.Comis.CLIScriptPath = program
	manifest.Comis.SecretResidencyScript = program
	manifest.GitHub.CLIPath = program
	manifest.GitHub.GitPath = program
	manifest.Services.SystemctlPath = program
	manifest.Comis.CodeRoot = root
	manifest.Comis.DataDir = root
	manifest.GitHub.PrimaryCheckout = root
	manifest.DevCrew.SocketPath = filepath.Join(root, "not-a-socket")
	for index := range manifest.Artifacts {
		path := filepath.Join(root, manifest.Artifacts[index].Kind)
		contents := []byte("artifact-" + manifest.Artifacts[index].Kind)
		if err := os.WriteFile(path, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest.Artifacts[index].Path = path
		manifest.Artifacts[index].SHA256 = sha256Hex(contents)
		switch manifest.Artifacts[index].Kind {
		case "comis-cli":
			manifest.Comis.CLIScriptPath = path
		case "devcrew":
			manifest.DevCrew.CLIPath = path
		}
	}
	for index := range manifest.Workers {
		path := filepath.Join(root, manifest.Workers[index].Kind)
		contents := []byte("worker-" + manifest.Workers[index].Kind)
		if err := os.WriteFile(path, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest.Workers[index].Path = path
		manifest.Workers[index].SHA256 = sha256Hex(contents)
	}
	if err := os.WriteFile(manifest.DevCrew.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntime(context.Background(), manifest, versionFixtureExecutor{
		manifest: manifest, comis: "1.0.61\n",
	}); err == nil || !strings.Contains(err.Error(), "Unix socket") {
		t.Fatalf("expected non-socket refusal, got %v", err)
	}
}

type versionFixtureExecutor struct {
	manifest Manifest
	comis    string
}

func (executor versionFixtureExecutor) Run(ctx context.Context, command Command) ([]byte, error) {
	if len(command.Args) == 2 && command.Args[1] == "--version" {
		return []byte(executor.comis), nil
	}
	if len(command.Args) == 2 && command.Args[0] == "cat" {
		switch command.Args[1] {
		case executor.manifest.Services.MCPUnit:
			return []byte("mcp unit\n"), nil
		case executor.manifest.Services.DevCrewUnit:
			return []byte("devcrew unit\n"), nil
		case executor.manifest.Services.ComisUnit:
			return []byte("comis unit\n"), nil
		}
	}
	if len(command.Args) == 1 && command.Args[0] == "--version" {
		switch filepath.Base(command.Path) {
		case "codex":
			return []byte("codex-cli 0.147.0\n"), nil
		case "claude":
			return []byte("2.1.224 (Claude Code)\n"), nil
		}
	}
	name := filepath.Base(command.Path)
	return []byte(name + " dev\n"), nil
}

func TestValidatePinnedArtifactRejectsChangedBytes(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "artifact")
	if err := os.WriteFile(path, []byte("expected"), 0o700); err != nil {
		t.Fatal(err)
	}
	pin := ArtifactPin{Kind: "devcrew", Path: path, SHA256: sha256Hex([]byte("expected")), Version: "dev"}
	if err := validatePinnedArtifact(pin); err != nil {
		t.Fatalf("validate unchanged artifact: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validatePinnedArtifact(pin); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected changed-artifact refusal, got %v", err)
	}
}

func TestValidatePinnedArtifactVersionRejectsDifferentExecutable(t *testing.T) {
	manifest := validManifest()
	pin := manifest.Artifacts[1]
	executor := &fixedOutputExecutor{output: []byte("devcrew unexpected\n")}
	if err := validatePinnedArtifactVersion(context.Background(), manifest, pin, executor); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("expected artifact-version refusal, got %v", err)
	}
	executor.output = []byte("devcrew dev\n")
	if err := validatePinnedArtifactVersion(context.Background(), manifest, pin, executor); err != nil {
		t.Fatalf("validate exact DevCrew version: %v", err)
	}
	if len(executor.seen) != 2 || len(executor.seen[1].Args) != 1 || executor.seen[1].Args[0] != "--version" {
		t.Fatalf("unexpected DevCrew version command: %#v", executor.seen)
	}

	comisPin := manifest.Artifacts[0]
	executor.output = []byte("1.0.61\n")
	if err := validatePinnedArtifactVersion(context.Background(), manifest, comisPin, executor); err != nil {
		t.Fatalf("validate exact Comis version: %v", err)
	}
	last := executor.seen[len(executor.seen)-1]
	if last.Path != manifest.Comis.NodePath || len(last.Args) != 2 || last.Args[0] != comisPin.Path || last.Args[1] != "--version" {
		t.Fatalf("unexpected Comis version command: %#v", last)
	}
}

func TestValidatePinnedServiceUnitRejectsChangedDefinition(t *testing.T) {
	manifest := validManifest()
	executor := &fixedOutputExecutor{output: []byte("changed unit\n")}
	if err := validatePinnedServiceUnit(
		context.Background(), manifest.Services.SystemctlPath, manifest.Services.MCPUnit,
		manifest.Services.MCPUnitSHA256, executor,
	); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected changed-unit refusal, got %v", err)
	}
	executor.output = []byte("mcp unit\n")
	if err := validatePinnedServiceUnit(
		context.Background(), manifest.Services.SystemctlPath, manifest.Services.MCPUnit,
		manifest.Services.MCPUnitSHA256, executor,
	); err != nil {
		t.Fatalf("validate exact service unit: %v", err)
	}
	if len(executor.seen) != 2 || strings.Join(executor.seen[1].Args, " ") != "cat "+manifest.Services.MCPUnit {
		t.Fatalf("unexpected service-unit command: %#v", executor.seen)
	}
}

func TestValidatePinnedWorkerRejectsVersionAndDigestDrift(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "codex")
	contents := []byte("worker")
	if err := os.WriteFile(path, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	pin := WorkerPin{Kind: "codex", ProfileID: "codex-reviewed", Path: path, SHA256: sha256Hex(contents), Version: "codex-cli 0.147.0"}
	executor := &fixedOutputExecutor{output: []byte("codex-cli 0.148.0\n")}
	if err := validatePinnedWorker(context.Background(), pin, executor); err == nil || !strings.Contains(err.Error(), "version") {
		t.Fatalf("expected worker-version refusal, got %v", err)
	}
	executor.output = []byte("codex-cli 0.147.0\n")
	if err := validatePinnedWorker(context.Background(), pin, executor); err != nil {
		t.Fatalf("validate exact worker: %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validatePinnedWorker(context.Background(), pin, executor); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("expected worker-digest refusal, got %v", err)
	}
}
