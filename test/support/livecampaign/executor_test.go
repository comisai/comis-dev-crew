package livecampaign

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
	if err := os.WriteFile(manifest.DevCrew.SocketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRuntime(manifest); err == nil || !strings.Contains(err.Error(), "Unix socket") {
		t.Fatalf("expected non-socket refusal, got %v", err)
	}
}
