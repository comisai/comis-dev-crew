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

func TestCLI_InitiativeWatchConsumesEventsAndRefreshesAuthoritativeDetail(t *testing.T) {
	client := fixtureClient()
	client.events = eventPageFixture()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{
		"initiative", "watch", "initiative-alpha", "--passes", "2", "--interval", "0s",
	}, &stdout, &stderr, testConfig(client))
	if code != ExitSuccess {
		t.Fatalf("Run(initiative watch) = %d, stderr=%q", code, stderr.String())
	}
	wantCalls := []string{
		"events:0:", "get-initiative:initiative-alpha",
		"events:9:", "get-initiative:initiative-alpha",
	}
	if strings.Join(client.calls, "|") != strings.Join(wantCalls, "|") {
		t.Fatalf("watch calls = %#v, want %#v", client.calls, wantCalls)
	}
	if strings.Count(stdout.String(), "INITIATIVE") != 2 {
		t.Fatalf("watch output did not refresh twice: %q", stdout.String())
	}
}

func TestCLI_InitiativeMutationsReturnPerMemberJSON(t *testing.T) {
	for _, test := range []struct {
		name     string
		command  string
		wantCall string
	}{
		{name: "pause", command: "pause", wantCall: "pause-initiative:initiative-alpha"},
		{name: "resume", command: "resume", wantCall: "resume-initiative:initiative-alpha"},
		{name: "cancel", command: "cancel", wantCall: "cancel-initiative:initiative-alpha"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := fixtureClient()
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), []string{
				"initiative", test.command, "initiative-alpha",
				"--operation", "operation-initiative-control", "--format", "json",
			}, &stdout, &stderr, testConfig(client))
			if code != ExitSuccess {
				t.Fatalf("Run(initiative %s) = %d, stderr=%q", test.command, code, stderr.String())
			}
			if len(client.calls) != 1 || client.calls[0] != test.wantCall ||
				client.operationID != "operation-initiative-control" {
				t.Fatalf("control calls/operation = %#v/%q", client.calls, client.operationID)
			}
			var decoded struct {
				InitiativeHandle string `json:"initiativeHandle"`
				Members          []struct {
					TaskHandle string `json:"taskHandle"`
					Outcome    string `json:"outcome"`
				} `json:"members"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil ||
				decoded.InitiativeHandle != "initiative-alpha" || len(decoded.Members) != 1 ||
				decoded.Members[0].Outcome != "rejected" {
				t.Fatalf("initiative control JSON = %#v, %v; raw=%q", decoded, err, stdout.String())
			}
		})
	}
}

func TestCLI_RejectsBroadenedInitiativeControlSyntaxBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		{"initiative", "watch", "initiative-alpha", "--passes", "0"},
		{"initiative", "pause", "initiative-alpha", "--task", "task-other"},
		{"initiative", "resume", "../escape"},
		{"initiative", "cancel", "initiative-alpha", "--discard", "true"},
		{"initiative", "cancel", "initiative-alpha", "--format", "table"},
	} {
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
				t.Fatal("invalid initiative control connected to the service")
			}
		})
	}
}
