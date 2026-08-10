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
	request := fmt.Sprintf(`{"version":"devcrew.runtime.v1","kind":"report","taskHandle":"task-runtime-0002","report":{"SchemaVersion":1}}` + "\n")
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

type runtimeHarness struct {
	server     *reporter.RuntimeServer
	client     *reporter.RuntimeClient
	sink       *recordingSink
	brief      domain.WorkerBrief
	socketPath string
}

func newRuntimeHarness(t *testing.T, taskHandle, localReportID string) runtimeHarness {
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
	socketPath := filepath.Join(root, "attachment.sock")
	server, err := reporter.ListenRuntime(reporter.RuntimeServerConfig{
		SocketPath: socketPath, Brief: brief, Reporter: reportClient,
	})
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
	return runtimeHarness{server: server, client: client, sink: sink, brief: brief, socketPath: socketPath}
}

func runtimeReport(brief domain.WorkerBrief, localReportID string) domain.WorkerReport {
	observed := time.Date(2026, time.August, 10, 12, 59, 0, 0, time.UTC)
	return domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: localReportID,
		BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
		Kind: domain.ReportProgress, Summary: "runtime attachment progress", WorkerObservedAt: &observed,
	}
}
