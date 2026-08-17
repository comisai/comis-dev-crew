package livecampaign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
	t.Setenv("GH_TOKEN", "github-token-must-not-leak")
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
	if err != nil || string(output) != "argv=print env=bounded github=false" {
		t.Fatalf("Run(print) = %q, %v", output, err)
	}
	output, err = executor.Run(context.Background(), Command{
		Path: executable,
		Args: []string{"-test.run=TestLiveCampaignExecutorHelperProcess", "--", "print"},
		Env: map[string]string{
			"GO_WANT_LIVE_CAMPAIGN_HELPER": "1", "LIVE_CAMPAIGN_TEST_VALUE": "bounded",
		},
		UseGitHubToken: true,
	})
	if err != nil || string(output) != "argv=print env=bounded github=true" {
		t.Fatalf("Run(GitHub print) = %q, %v", output, err)
	}
	if _, err := executor.Run(context.Background(), Command{
		Path: executable, Env: map[string]string{"GH_TOKEN": "forbidden-override"},
	}); err == nil {
		t.Fatal("Run(GH_TOKEN override) error = nil")
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

func TestRealExecutorScopesGatewayCredentialToRequestedCommand(t *testing.T) {
	t.Setenv("COMIS_GATEWAY_TOKEN", "gateway-token-must-not-leak")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := Command{
		Path: executable,
		Args: []string{"-test.run=TestLiveCampaignExecutorHelperProcess", "--", "credential"},
		Env:  map[string]string{"GO_WANT_LIVE_CAMPAIGN_HELPER": "1"},
	}
	credentialField := reflect.ValueOf(&command).Elem().FieldByName("UseComisGatewayToken")
	if !credentialField.IsValid() || !credentialField.CanSet() || credentialField.Kind() != reflect.Bool {
		t.Fatal("Command.UseComisGatewayToken is unavailable")
	}
	credentialField.SetBool(true)
	output, err := (RealExecutor{}).Run(context.Background(), command)
	if err != nil || string(output) != "gateway=true" {
		t.Fatalf("Run(gateway credential) = %q, %v", output, err)
	}
	t.Setenv("COMIS_GATEWAY_TOKEN", "")
	if _, err := (RealExecutor{}).Run(context.Background(), command); err == nil ||
		!strings.Contains(err.Error(), "gateway credential is unavailable") {
		t.Fatalf("Run(missing gateway credential) error = %v", err)
	}
}

func TestRealExecutorForwardsDeclaredCampaignSecretsToRequestedCommand(t *testing.T) {
	t.Setenv("TELEGRAM_BOT_TOKEN", "campaign-secret-must-not-leak")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := Command{
		Path: executable,
		Args: []string{"-test.run=TestLiveCampaignExecutorHelperProcess", "--", "residency"},
		Env:  map[string]string{"GO_WANT_LIVE_CAMPAIGN_HELPER": "1"},
	}
	names := reflect.ValueOf(&command).Elem().FieldByName("SecretEnvironmentNames")
	if !names.IsValid() || !names.CanSet() || names.Kind() != reflect.Slice {
		t.Fatal("Command.SecretEnvironmentNames is unavailable")
	}
	names.Set(reflect.ValueOf([]string{"TELEGRAM_BOT_TOKEN"}))
	output, err := (RealExecutor{}).Run(context.Background(), command)
	if err != nil || string(output) != "residency=true" {
		t.Fatalf("Run(declared campaign secret) = %q, %v", output, err)
	}
	names.Set(reflect.ValueOf([]string{"PATH"}))
	if _, err := (RealExecutor{}).Run(context.Background(), command); err == nil ||
		!strings.Contains(err.Error(), "secret name is invalid") {
		t.Fatalf("Run(protected inherited name) error = %v", err)
	}
	names.Set(reflect.ValueOf([]string{"telegram.bot.token"}))
	if _, err := (RealExecutor{}).Run(context.Background(), command); err == nil ||
		!strings.Contains(err.Error(), "secret name is invalid") {
		t.Fatalf("Run(non-environment secret name) error = %v", err)
	}
	names.Set(reflect.ValueOf([]string{"COMIS_TELEGRAM_ABSENT_TOKEN"}))
	output, err = (RealExecutor{}).Run(context.Background(), command)
	if err != nil || string(output) != "residency=false" {
		t.Fatalf("Run(absent campaign secret) = %q, %v", output, err)
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
		fmt.Printf("argv=print env=%s github=%t", os.Getenv("LIVE_CAMPAIGN_TEST_VALUE"), os.Getenv("GH_TOKEN") != "")
	case "oversized":
		fmt.Print(strings.Repeat("x", maximumCommandOutputBytes+1))
	case "fail":
		fmt.Fprint(os.Stderr, "sensitive-child-detail")
		os.Exit(7)
	case "credential":
		fmt.Printf("gateway=%t", os.Getenv("COMIS_GATEWAY_TOKEN") != "")
	case "residency":
		fmt.Printf("residency=%t", os.Getenv("TELEGRAM_BOT_TOKEN") != "")
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
	manifest.DevCrew.CodeRoot = filepath.Join(root, "devcrew-source")
	if err := os.Mkdir(manifest.DevCrew.CodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest.DevCrew.DatabasePath = filepath.Join(root, "devcrew.db")
	if err := os.WriteFile(manifest.DevCrew.DatabasePath, []byte("database-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.DevCrew.WorktreeRoot = filepath.Join(root, "worktrees")
	if err := os.Mkdir(manifest.DevCrew.WorktreeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest.Recovery.CandidateConfigPath = filepath.Join(root, "candidate.json")
	if err := os.WriteFile(manifest.Recovery.CandidateConfigPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest.Comis.DataDir = root
	manifest.Comis.DatabasePath = filepath.Join(root, "comis.db")
	if err := os.WriteFile(manifest.Comis.DatabasePath, []byte("database-fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
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
	for index := range manifest.Recovery.PreviousArtifacts {
		path := filepath.Join(root, "previous-"+manifest.Recovery.PreviousArtifacts[index].Kind)
		contents := []byte("previous-" + manifest.Recovery.PreviousArtifacts[index].Kind)
		if err := os.WriteFile(path, contents, 0o700); err != nil {
			t.Fatal(err)
		}
		manifest.Recovery.PreviousArtifacts[index].Path = path
		manifest.Recovery.PreviousArtifacts[index].SHA256 = sha256Hex(contents)
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
		if command.Args[0] == executor.manifest.Comis.CLIScriptPath {
			return []byte(executor.comis), nil
		}
		for _, artifact := range executor.manifest.Recovery.PreviousArtifacts {
			if artifact.Kind == "comis-cli" && command.Args[0] == artifact.Path {
				return []byte(artifact.Version + "\n"), nil
			}
		}
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
		for _, artifact := range append(
			append([]ArtifactPin(nil), executor.manifest.Artifacts...),
			executor.manifest.Recovery.PreviousArtifacts...,
		) {
			if artifact.Kind != "comis-cli" && artifact.Path == command.Path {
				return []byte(artifact.Kind + " " + artifact.Version + "\n"), nil
			}
		}
		switch filepath.Base(command.Path) {
		case "codex":
			return []byte("codex-cli 0.147.0\n"), nil
		case "claude":
			return []byte("2.1.224 (Claude Code)\n"), nil
		}
	}
	if len(command.Args) == 4 && command.Args[0] == "-C" && command.Args[2] == "rev-parse" && command.Args[3] == "HEAD" {
		if command.Args[1] == executor.manifest.Comis.CodeRoot {
			return []byte(executor.manifest.Source.ComisCommit + "\n"), nil
		}
		if command.Args[1] == executor.manifest.DevCrew.CodeRoot {
			return []byte(executor.manifest.Source.DevCrewCommit + "\n"), nil
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

func TestValidatePinnedSourceRejectsDifferentCheckoutHead(t *testing.T) {
	manifest := validManifest()
	executor := &fixedOutputExecutor{output: []byte(strings.Repeat("a", 40) + "\n")}
	if err := validatePinnedSource(
		context.Background(), manifest.GitHub.GitPath, manifest.Comis.CodeRoot,
		manifest.Source.ComisCommit, executor,
	); err == nil || !strings.Contains(err.Error(), "source HEAD") {
		t.Fatalf("expected source-head refusal, got %v", err)
	}
	executor.output = []byte(manifest.Source.ComisCommit + "\n")
	if err := validatePinnedSource(
		context.Background(), manifest.GitHub.GitPath, manifest.Comis.CodeRoot,
		manifest.Source.ComisCommit, executor,
	); err != nil {
		t.Fatalf("validate exact source HEAD: %v", err)
	}
	if len(executor.seen) != 2 || strings.Join(executor.seen[1].Args, " ") != "-C "+manifest.Comis.CodeRoot+" rev-parse HEAD" {
		t.Fatalf("unexpected source command: %#v", executor.seen)
	}
}
