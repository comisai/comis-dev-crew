//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/mcpadapter"
	"github.com/comisai/comis-dev-crew/internal/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const replacementOperationID = "replacement-prepare-0001"

func TestMCPReplacement_ProcessCrashPreservesPreparedTaskAndOperation(t *testing.T) {
	binary := buildReplacementMCP(t)
	operatorSocket, mcpSocket := startReplacementService(t)
	firstSession, firstCommand, firstStderr := connectReplacementMCP(t, binary, mcpSocket)
	first, err := firstSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta(replacementOperationID), Name: mcpadapter.ToolPrepareTask,
		Arguments: parityMCPPrepareInput(),
	})
	if err != nil || first.IsError {
		text := ""
		if first != nil && len(first.Content) != 0 {
			if content, ok := first.Content[0].(*mcp.TextContent); ok {
				text = content.Text
			}
		}
		t.Fatalf("first prepare = %#v text=%q, %v, stderr=%q", first, text, err, firstStderr.String())
	}
	if err := firstCommand.Process.Kill(); err != nil {
		t.Fatalf("kill first devcrew-mcp: %v", err)
	}
	_ = firstSession.Close()

	secondSession, _, secondStderr := connectReplacementMCP(t, binary, mcpSocket)
	t.Cleanup(func() { _ = secondSession.Close() })
	forgedMeta := parityCallMeta(replacementOperationID)
	callContext := forgedMeta[mcpadapter.CallContextMetaKey].(map[string]any)
	callContext["managedRunId"] = "managed-run-forged"
	second, err := secondSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: forgedMeta, Name: mcpadapter.ToolPrepareTask, Arguments: parityMCPPrepareInput(),
	})
	if err != nil || second.IsError {
		t.Fatalf("replacement prepare = %#v, %v, stderr=%q", second, err, secondStderr.String())
	}
	if !reflect.DeepEqual(first.StructuredContent, second.StructuredContent) ||
		!reflect.DeepEqual(first.Meta, second.Meta) {
		t.Fatalf("replacement replay differs: first=%#v second=%#v", first, second)
	}

	listed, err := secondSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta("replacement-list-0001"), Name: mcpadapter.ToolListTasks,
		Arguments: mcpadapter.EmptyInput{},
	})
	if err != nil || listed.IsError {
		t.Fatalf("replacement list = %#v, %v", listed, err)
	}
	var taskList application.TaskList
	decodeStructured(t, listed.StructuredContent, &taskList)
	if len(taskList.Tasks) != 1 || taskList.Tasks[0].TaskHandle != "task-replacement-0001" ||
		taskList.Tasks[0].State != domain.TaskPrepared {
		t.Fatalf("task list after replacement = %#v", taskList)
	}
	got, err := secondSession.CallTool(context.Background(), &mcp.CallToolParams{
		Meta: parityCallMeta("replacement-get-0001"), Name: mcpadapter.ToolGetTask,
		Arguments: mcpadapter.TaskInput{TaskHandle: "task-replacement-0001"},
	})
	if err != nil || got.IsError {
		t.Fatalf("replacement get = %#v, %v", got, err)
	}
	var task application.TaskDetail
	decodeStructured(t, got.StructuredContent, &task)
	if task.Summary.TaskHandle != "task-replacement-0001" || task.Summary.State != domain.TaskPrepared {
		t.Fatalf("task after replacement = %#v", task)
	}

	operator, err := localapi.NewClient(operatorSocket, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := operator.Operation(context.Background(), "replacement-operation-read", replacementOperationID)
	if err != nil || operation.Status != domain.OperationCompleted || operation.Command != "PrepareTask" {
		t.Fatalf("durable operation after replacement = %#v, %v", operation, err)
	}
}

func buildReplacementMCP(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "devcrew-mcp")
	build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/devcrew-mcp")
	build.Dir = integrationRepositoryRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build devcrew-mcp: %v\n%s", err, output)
	}
	return binary
}

func startReplacementService(t *testing.T) (string, string) {
	t.Helper()
	root := integrationShortTempDir(t)
	operatorSocket := filepath.Join(root, "run", "operator.sock")
	mcpSocket := filepath.Join(root, "run", "mcp.sock")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	now := time.Now().UTC()
	go func() {
		done <- service.Run(ctx, service.Config{
			DatabasePath: filepath.Join(root, "state", "devcrew.db"),
			SocketPath:   operatorSocket, MCPSocketPath: mcpSocket, ServiceInstanceID: parityServiceID,
			Repositories: parityRepositoryCatalog{}, TaskIDs: func() (string, error) { return "task-replacement-0001", nil },
			RegistrationNonces: func() (string, error) { return "registration-nonce_replacement", nil },
			PreparationTTL:     time.Hour, Clock: func() time.Time { return now }, Ready: func() { close(ready) },
		})
	}()
	select {
	case <-ready:
	case err := <-done:
		cancel()
		t.Fatalf("replacement service stopped before ready: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("replacement service readiness timed out")
	}
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("replacement service stop error = %v", err)
		}
	})
	return operatorSocket, mcpSocket
}

func connectReplacementMCP(t *testing.T, binary, socket string) (*mcp.ClientSession, *exec.Cmd, *bytes.Buffer) {
	t.Helper()
	command := exec.Command(binary, "--socket", socket, "--service-instance", parityServiceID)
	stderr := &bytes.Buffer{}
	command.Stderr = stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "replacement-client", Version: "integration"}, nil)
	session, err := client.Connect(context.Background(), &mcp.CommandTransport{
		Command: command, TerminateDuration: 100 * time.Millisecond,
	}, nil)
	if err != nil {
		t.Fatalf("connect devcrew-mcp: %v, stderr=%q", err, stderr.String())
	}
	return session, command, stderr
}
