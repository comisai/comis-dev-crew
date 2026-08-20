package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCLI_InitiativeReadsUseCanonicalClientAndHumanViews(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCall   string
		wantOutput string
	}{
		{name: "list", args: []string{"initiative", "list", "--state", "active"}, wantCall: "list-initiatives:active", wantOutput: "INITIATIVE"},
		{name: "show", args: []string{"initiative", "show", "initiative-alpha"}, wantCall: "get-initiative:initiative-alpha", wantOutput: "initiative-alpha"},
		{name: "explain", args: []string{"initiative", "explain", "initiative-alpha"}, wantCall: "get-initiative:initiative-alpha", wantOutput: "REASON"},
		{name: "graph", args: []string{"initiative", "graph", "initiative-alpha"}, wantCall: "get-initiative:initiative-alpha", wantOutput: "DEPENDENCY READY"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureClient()
			var stdout, stderr bytes.Buffer
			if code := Run(context.Background(), test.args, &stdout, &stderr, testConfig(client)); code != ExitSuccess {
				t.Fatalf("Run(%v) = %d, stderr=%q", test.args, code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantOutput) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.wantOutput)
			}
			if len(client.calls) != 1 || client.calls[0] != test.wantCall {
				t.Fatalf("client calls = %#v, want %q", client.calls, test.wantCall)
			}
		})
	}
}

func TestCLI_InitiativeGraphJSONReturnsTheGraphProjectionItself(t *testing.T) {
	client := fixtureClient()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"initiative", "graph", "initiative-alpha", "--format", "json",
	}, &stdout, &stderr, testConfig(client))
	if code != ExitSuccess {
		t.Fatalf("Run(initiative graph JSON) = %d, stderr=%q", code, stderr.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		t.Fatalf("graph JSON = %q: %v", stdout.String(), err)
	}
	if decoded["initiativeHandle"] != "initiative-alpha" || decoded["nodes"] == nil || decoded["edges"] == nil {
		t.Fatalf("graph projection = %#v", decoded)
	}
	if decoded["initiative"] != nil || decoded["graph"] != nil {
		t.Fatalf("graph JSON wrapped the projection: %#v", decoded)
	}
}

func TestCLI_RejectsInvalidInitiativeSyntaxBeforeConnecting(t *testing.T) {
	tests := [][]string{
		{"initiative"},
		{"initiative", "list", "--state", "invented"},
		{"initiative", "list", "--format", "yaml"},
		{"initiative", "show", "../escape"},
		{"initiative", "show", "initiative-alpha", "--format", "yaml"},
		{"initiative", "graph", "initiative-alpha", "extra"},
		{"initiative", "delete", "initiative-alpha"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			factoryCalled := false
			config := testConfig(fixtureClient())
			config.NewClient = func(string) (ReadClient, error) {
				factoryCalled = true
				return fixtureClient(), nil
			}
			var output bytes.Buffer
			if code := Run(context.Background(), args, &output, &output, config); code != ExitUsage {
				t.Fatalf("Run(%v) = %d, want %d", args, code, ExitUsage)
			}
			if factoryCalled {
				t.Fatal("invalid initiative command connected to the service")
			}
		})
	}
}
