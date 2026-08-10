package reporter_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func TestRuntimeAttachment_ServesPinnedBriefAndDerivesReportTaskFromSocket(t *testing.T) {
	first := newRuntimeHarness(t, "task-runtime-0001", "report-runtime-0001")
	second := newRuntimeHarness(t, "task-runtime-0002", "report-runtime-0002")

	brief, err := first.client.Brief(context.Background())
	if err != nil || brief != first.brief {
		t.Fatalf("Brief(first) = %#v, %v, want %#v", brief, err, first.brief)
	}
	report := runtimeReport(first.brief, "report-runtime-0001")
	receipt, err := first.client.Report(context.Background(), report)
	if err != nil {
		t.Fatalf("Report(first) error = %v", err)
	}
	if receipt.TaskHandle != "task-runtime-0001" || first.sink.calls != 1 || second.sink.calls != 0 ||
		first.sink.accepted.TaskHandle != "task-runtime-0001" {
		t.Fatalf("task-scoped report = receipt:%#v first:%#v second calls:%d", receipt, first.sink.accepted, second.sink.calls)
	}

	if err := first.server.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(first.socketPath); !os.IsNotExist(err) {
		t.Fatalf("closed first attachment remains: %v", err)
	}
	if brief, err := second.client.Brief(context.Background()); err != nil || brief != second.brief {
		t.Fatalf("stopping first attachment affected second: %#v, %v", brief, err)
	}
}

func TestRuntimeAttachment_RejectsCrossTaskAndOversizedWireRequests(t *testing.T) {
	harness := newRuntimeHarness(t, "task-runtime-0001", "report-runtime-0001")
	connection, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: harness.socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	request := `{"version":"devcrew.runtime.v1","kind":"report","taskHandle":"task-runtime-0002","report":{"SchemaVersion":1}}` + "\n"
	if _, err := connection.Write([]byte(request)); err != nil {
		t.Fatal(err)
	}
	var outcome reporter.RuntimeOutcome
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&outcome); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if outcome.Error == nil || harness.sink.calls != 0 {
		t.Fatalf("cross-task wire request = %#v, sink calls=%d", outcome, harness.sink.calls)
	}

	forged := harness.expectedLaunch
	forged.TaskHandle = "task-runtime-0002"
	encoded, err := json.Marshal(map[string]any{
		"version": "devcrew.runtime.v1", "kind": "acknowledge", "acknowledgement": forged,
	})
	if err != nil {
		t.Fatal(err)
	}
	connection, err = net.DialUnix("unix", nil, &net.UnixAddr{Name: harness.socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(append(encoded, '\n')); err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&outcome); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if outcome.Error == nil || harness.acknowledger.calls != 0 {
		t.Fatalf("forged launch acknowledgement = %#v, mutation calls=%d", outcome, harness.acknowledger.calls)
	}

	connection, err = net.DialUnix("unix", nil, &net.UnixAddr{Name: harness.socketPath, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Write(append([]byte(`{"version":"devcrew.runtime.v1","kind":"brief","padding":"`), append([]byte(strings.Repeat("x", 20*1024)), []byte(`"}`+"\n")...)...)); err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(bufio.NewReader(connection)).Decode(&outcome); err != nil {
		t.Fatal(err)
	}
	_ = connection.Close()
	if outcome.Error == nil {
		t.Fatal("oversized runtime request was accepted")
	}
}

func TestRuntimeAttachment_AcknowledgesOnlyExactProtectedLaunchBinding(t *testing.T) {
	first := newRuntimeHarness(t, "task-runtime-ack-0001", "report-runtime-ack-0001")
	second := newRuntimeHarness(t, "task-runtime-ack-0002", "report-runtime-ack-0002")

	if err := first.client.Acknowledge(context.Background(), first.workspace); err != nil {
		t.Fatalf("Acknowledge(first) error = %v", err)
	}
	if first.acknowledger.calls != 1 || second.acknowledger.calls != 0 ||
		first.acknowledger.command.OperationID != first.launchOperationID ||
		first.acknowledger.command.Acknowledgement != first.expectedLaunch {
		t.Fatalf("protected acknowledgement = first:%#v second calls:%d", first.acknowledger, second.acknowledger.calls)
	}
	if err := first.client.Acknowledge(context.Background(), filepath.Join(first.workspace, "other")); err == nil {
		t.Fatal("Acknowledge(wrong cwd) error = nil")
	}
	if first.acknowledger.calls != 1 {
		t.Fatalf("wrong cwd reached mutation authority: calls=%d", first.acknowledger.calls)
	}
	if err := first.server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := second.client.Acknowledge(context.Background(), second.workspace); err != nil || second.acknowledger.calls != 1 {
		t.Fatalf("stopping first affected second acknowledgement: %v calls=%d", err, second.acknowledger.calls)
	}
}

func TestRuntimeAttachment_BindsActivationLaunchWithoutReplacingPreparedSocket(t *testing.T) {
	harness := newRuntimeHarnessWithLaunch(t, "task-runtime-late-0001", "report-runtime-late-0001", false)
	before, err := os.Lstat(harness.socketPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.client.Acknowledge(context.Background(), harness.workspace); err == nil {
		t.Fatal("Acknowledge(before activation binding) error = nil")
	}
	binding := reporter.RuntimeLaunchConfig{
		OperationID: harness.launchOperationID, Expected: harness.expectedLaunch,
		Acknowledger: harness.acknowledger,
	}
	if err := harness.server.BindLaunch(binding); err != nil {
		t.Fatalf("BindLaunch() error = %v", err)
	}
	after, err := os.Lstat(harness.socketPath)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("prepared socket identity changed: %v", err)
	}
	if err := harness.client.Acknowledge(context.Background(), harness.workspace); err != nil {
		t.Fatalf("Acknowledge(after activation binding) error = %v", err)
	}
	if err := harness.server.BindLaunch(binding); err != nil {
		t.Fatalf("BindLaunch(identical replay) error = %v", err)
	}
	altered := binding
	altered.OperationID = "operation-launch-ack-altered"
	if err := harness.server.BindLaunch(altered); err == nil {
		t.Fatal("BindLaunch(altered replay) error = nil")
	}
}

type runtimeHarness struct {
	server            *reporter.RuntimeServer
	client            *reporter.RuntimeClient
	sink              *recordingSink
	brief             domain.WorkerBrief
	socketPath        string
	workspace         string
	expectedLaunch    application.LaunchAcknowledgement
	launchOperationID string
	acknowledger      *recordingLaunchAcknowledger
}

func newRuntimeHarness(t *testing.T, taskHandle, localReportID string) runtimeHarness {
	return newRuntimeHarnessWithLaunch(t, taskHandle, localReportID, true)
}

func newRuntimeHarnessWithLaunch(t *testing.T, taskHandle, localReportID string, bindLaunch bool) runtimeHarness {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "devcrew-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	content := "taskHandle: " + taskHandle + "\nacceptanceCriteria:\n- prove runtime attachment\n"
	brief := domain.WorkerBrief{
		Revision: 1, RevisionHash: fmt.Sprintf("%x", sha256.Sum256([]byte(content))), Content: content,
	}
	sink := &recordingSink{receipt: domain.ReportReceipt{
		TaskHandle: taskHandle, LocalReportID: localReportID, StateVersion: 3,
		AcceptedAt: time.Date(2026, time.August, 10, 13, 0, 0, 0, time.UTC),
	}}
	endpoint, err := reporter.NewEndpoint(reporter.EndpointConfig{
		TaskHandle: taskHandle, BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
		Credential: validCredential, Sink: sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	reportClient, err := reporter.NewClient(endpoint, validCredential)
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	expectedLaunch := application.LaunchAcknowledgement{
		TaskHandle: taskHandle, ManagedRunID: "managed-run-" + taskHandle,
		WorkspaceLeaseID: "workspace-lease-" + taskHandle, WorkingDirectory: workspace,
		BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
	}
	launchOperationID := "operation-launch-ack-" + taskHandle
	acknowledger := &recordingLaunchAcknowledger{}
	socketPath := filepath.Join(root, "attachment.sock")
	config := reporter.RuntimeServerConfig{SocketPath: socketPath, Brief: brief, Reporter: reportClient}
	if bindLaunch {
		config.LaunchOperationID = launchOperationID
		config.ExpectedLaunch = expectedLaunch
		config.LaunchAcknowledger = acknowledger
	}
	server, err := reporter.ListenRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		if err := <-done; err != nil {
			t.Errorf("runtime server stop error = %v", err)
		}
	})
	client, err := reporter.NewRuntimeClient(socketPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	return runtimeHarness{
		server: server, client: client, sink: sink, brief: brief, socketPath: socketPath,
		workspace: workspace, expectedLaunch: expectedLaunch,
		launchOperationID: launchOperationID, acknowledger: acknowledger,
	}
}

type recordingLaunchAcknowledger struct {
	command application.AcknowledgeWorkerLaunchCommand
	calls   int
}

func (acknowledger *recordingLaunchAcknowledger) AcknowledgeWorkerLaunch(
	_ context.Context,
	command application.AcknowledgeWorkerLaunchCommand,
) (application.MutationResult, error) {
	acknowledger.command = command
	acknowledger.calls++
	return application.MutationResult{
		Task: domain.Task{
			Handle: command.Acknowledgement.TaskHandle, ManagedRunID: command.Acknowledgement.ManagedRunID,
			WorkspaceLeaseID: command.Acknowledgement.WorkspaceLeaseID, State: domain.TaskWorking,
			BriefRevision:     command.Acknowledgement.BriefRevision,
			BriefRevisionHash: command.Acknowledgement.BriefRevisionHash,
		},
		Operation: domain.OperationRecord{ID: command.OperationID, Status: domain.OperationCompleted},
	}, nil
}

func runtimeReport(brief domain.WorkerBrief, localReportID string) domain.WorkerReport {
	observed := time.Date(2026, time.August, 10, 12, 59, 0, 0, time.UTC)
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: localReportID,
		BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
		Kind: domain.ReportProgress, Summary: "runtime attachment progress", WorkerObservedAt: &observed,
	}
}
