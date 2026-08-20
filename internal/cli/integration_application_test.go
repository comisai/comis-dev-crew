package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCLIInitiativeIntegrationCommandIsReachable(t *testing.T) {
	config := testConfig(fixtureClient())
	config.Stdin = strings.NewReader(`{
  "integrationTaskHandle":"task-integration",
  "candidateTaskHandle":"task-candidate",
  "candidateHead":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "expectedIntegrationHead":"cccccccccccccccccccccccccccccccccccccccc"
}`)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"initiative", "integrate", "initiative-cli", "--input", "-",
		"--operation", "operation-integration-cli", "--format", "json",
	}, &stdout, &stderr, config)
	if code == ExitUsage {
		t.Fatalf("initiative integration command is unreachable: stderr=%q", stderr.String())
	}
}
