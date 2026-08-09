package mcpadapter

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRunCommand_HelpVersionAndStrictArguments(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantExit   int
		wantOutput string
		wantError  string
	}{
		{name: "help", args: []string{"--help"}, wantExit: 0, wantOutput: "Usage: devcrew-mcp"},
		{name: "short help", args: []string{"-h"}, wantExit: 0, wantOutput: "Usage: devcrew-mcp"},
		{name: "version", args: []string{"--version"}, wantExit: 0, wantOutput: "devcrew-mcp test-version"},
		{name: "unknown", args: []string{"--private-token-value"}, wantExit: 2, wantError: "invalid MCP arguments"},
		{name: "positional", args: []string{"private-positional"}, wantExit: 2, wantError: "invalid MCP arguments"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			called := false
			exit := RunCommand(context.Background(), test.args, &stdout, &stderr, CommandConfig{
				DefaultSocketPath: "/private/tmp/mcp.sock", DefaultServiceInstanceID: "service-instance-0001",
				Version: "test-version", NewClient: func(string) (Client, error) { called = true; return &fakeClient{}, nil },
				NewOperationID: func() (string, error) { return "reconcile-0001", nil },
				Transport:      failingTransport{},
			})
			if exit != test.wantExit || !strings.Contains(stdout.String(), test.wantOutput) || !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("RunCommand() = %d, stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
			if called {
				t.Fatal("NewClient called for informational or invalid command")
			}
			if strings.Contains(stdout.String()+stderr.String(), "private-token-value") || strings.Contains(stdout.String()+stderr.String(), "private-positional") {
				t.Fatal("diagnostic leaked rejected argument")
			}
		})
	}
}

func TestRunCommand_ComposesExplicitStatelessFacade(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	wantSocket := "/private/tmp/explicit-mcp.sock"
	wantService := "service-instance-explicit"
	client := &fakeClient{}
	clientPath := ""
	run := false
	exit := RunCommand(context.Background(), []string{
		"--socket", wantSocket, "--service-instance", wantService,
	}, &stdout, &stderr, CommandConfig{
		DefaultSocketPath: "/private/tmp/default.sock", DefaultServiceInstanceID: "service-instance-default",
		Version:        "test-version",
		NewClient:      func(path string) (Client, error) { clientPath = path; return client, nil },
		NewOperationID: func() (string, error) { return "reconcile-0001", nil },
		Transport:      failingTransport{},
		RunFacade: func(ctx context.Context, facade *Facade, transport mcp.Transport) error {
			run = ctx != nil && facade != nil && transport != nil
			return nil
		},
	})
	if exit != 0 || clientPath != wantSocket || !run || stdout.Len() != 0 || stderr.Len() != 0 {
		t.Fatalf("RunCommand() = %d path=%q run=%v stdout=%q stderr=%q", exit, clientPath, run, stdout.String(), stderr.String())
	}
}

func TestRunCommand_FailsClosedWithoutLeakingCompositionDetails(t *testing.T) {
	private := errors.New("private local socket and transport detail")
	tests := []struct {
		name   string
		config CommandConfig
	}{
		{name: "missing paths", config: CommandConfig{}},
		{name: "client failure", config: CommandConfig{
			DefaultSocketPath: "/private/tmp/private.sock", DefaultServiceInstanceID: "service-instance-0001",
			NewClient:      func(string) (Client, error) { return nil, private },
			NewOperationID: func() (string, error) { return "reconcile-0001", nil }, Transport: failingTransport{},
		}},
		{name: "facade failure", config: CommandConfig{
			DefaultSocketPath: "/private/tmp/private.sock", DefaultServiceInstanceID: "bad service",
			NewClient:      func(string) (Client, error) { return &fakeClient{}, nil },
			NewOperationID: func() (string, error) { return "reconcile-0001", nil }, Transport: failingTransport{},
		}},
		{name: "transport failure", config: CommandConfig{
			DefaultSocketPath: "/private/tmp/private.sock", DefaultServiceInstanceID: "service-instance-0001",
			NewClient:      func(string) (Client, error) { return &fakeClient{}, nil },
			NewOperationID: func() (string, error) { return "reconcile-0001", nil }, Transport: failingTransport{err: private},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exit := RunCommand(context.Background(), nil, &stdout, &stderr, test.config)
			if exit == 0 || strings.Contains(stdout.String()+stderr.String(), private.Error()) || strings.Contains(stderr.String(), "private.sock") {
				t.Fatalf("RunCommand() = %d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
			}
		})
	}
}
