package workers_test

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func TestCodexAdapter_ProbesAndPinsExactInstalledVersion(t *testing.T) {
	profile := probedCodexProfile(t, "codex-probed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := adapter.ProbeVersion(context.Background())
	if err != nil || probe.Version != "codex-cli 0.147.0" ||
		probe.Availability != application.HarnessAvailable || probe.Reason != "" {
		t.Fatalf("ProbeVersion() = %#v, %v", probe, err)
	}

	mismatched, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.148.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err = mismatched.ProbeVersion(context.Background())
	if err != nil || probe.Availability != application.HarnessUnavailable ||
		probe.Reason != application.HarnessReasonVersionMismatch {
		t.Fatalf("ProbeVersion(mismatch) = %#v, %v", probe, err)
	}
}

func TestCodexAdapter_BuildsProtectedAttachmentLaunchWithoutTaskAuthorityInArgv(t *testing.T) {
	profile := availableCodexProfile(codexFixtureExecutable(t), "codex-reviewed")
	profile.Unattended = true
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0",
		SettleSignalVerified: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	hostSocket, closeSocket := codexAttachmentSocket(t)
	defer closeSocket()
	request := application.WorkerLaunchRequest{
		ProfileID: profile.ID, Shape: domain.ShapeShip, WorkingDirectory: canonicalWorkerTempDir(t),
		TaskHandle: "task-secret-0001", ManagedRunID: "managed-run-secret-0001",
		WorkspaceLeaseID: "workspace-lease-secret-0001", BriefRevision: 3,
		BriefRevisionHash: strings.Repeat("a", 64),
		Attachment: application.RuntimeSocketAttachment{
			HostSocketPath: hostSocket, MountSocketPath: "/run/devcrew/attachment.sock",
		},
	}
	descriptor, err := adapter.BuildLaunchDescriptor(context.Background(), request)
	if err != nil {
		t.Fatalf("BuildLaunchDescriptor() error = %v", err)
	}
	if descriptor.Executable != profile.Executable || descriptor.WorkingDirectory != request.WorkingDirectory ||
		descriptor.Attachment != request.Attachment || descriptor.Unattended ||
		descriptor.DegradedReason != application.HarnessReasonLifecycleSignalUnknown {
		t.Fatalf("Codex launch descriptor = %#v", descriptor)
	}
	wantArguments := []string{
		"exec", "--json", "--strict-config", "--ignore-user-config", "--ignore-rules", "--ephemeral",
		"--color", "never", "--model", profile.Model, "--sandbox", "workspace-write",
		"-c", `model_reasoning_effort="high"`, "--cd", request.WorkingDirectory, "-",
	}
	if strings.Join(descriptor.Arguments, "\x00") != strings.Join(wantArguments, "\x00") {
		t.Fatalf("Codex argv = %q, want %q", descriptor.Arguments, wantArguments)
	}
	joined := strings.Join(descriptor.Arguments, "\x00") + string(descriptor.StandardInput)
	for _, secret := range []string{
		request.TaskHandle, request.ManagedRunID, request.WorkspaceLeaseID,
		request.BriefRevisionHash, hostSocket,
	} {
		if strings.Contains(joined, secret) {
			t.Fatalf("Codex process input leaked task authority %q", secret)
		}
	}
	if !strings.Contains(string(descriptor.StandardInput), "devcrew-report brief") ||
		!containsString(descriptor.EnvironmentKeys, "DEV_CREW_ATTACHMENT") {
		t.Fatalf("Codex bootstrap does not use protected attachment: %#v", descriptor)
	}
	if descriptor.ExpectedAcknowledgement.TaskHandle != request.TaskHandle ||
		descriptor.ExpectedAcknowledgement.ManagedRunID != request.ManagedRunID ||
		descriptor.ExpectedAcknowledgement.WorkspaceLeaseID != request.WorkspaceLeaseID ||
		descriptor.ExpectedAcknowledgement.WorkingDirectory != request.WorkingDirectory ||
		descriptor.ExpectedAcknowledgement.BriefRevisionHash != request.BriefRevisionHash {
		t.Fatalf("expected launch acknowledgement = %#v", descriptor.ExpectedAcknowledgement)
	}
}

func TestCodexAdapter_ClassifiesOnlyFreshStructuredSemanticEvidence(t *testing.T) {
	profile := availableCodexProfile(codexFixtureExecutable(t), "codex-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0",
		SettleSignalVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		observation application.HarnessObservation
		want        application.SemanticActivity
		reason      application.SemanticReason
	}{
		{name: "turn started", observation: codexObservation(now, `{"type":"turn.started"}`), want: application.ActivityBusy},
		{name: "item started", observation: codexObservation(now, `{"type":"item.started","item":{"type":"command_execution"}}`), want: application.ActivityBusy},
		{name: "input request", observation: codexObservation(now, `{"type":"request.user_input"}`), want: application.ActivityAwaitingInput},
		{name: "turn completed without report", observation: codexObservation(now, `{"type":"turn.completed"}`), want: application.ActivityUnknown, reason: application.SemanticReasonSettledWithoutReport},
		{name: "process exited", observation: application.HarnessObservation{Process: application.ProcessExited, ObservedAt: now, Now: now}, want: application.ActivityExited},
		{name: "malformed", observation: codexObservation(now, `{`), want: application.ActivityUnknown, reason: application.SemanticReasonMalformed},
		{name: "unsupported", observation: codexObservation(now, `{"type":"invented"}`), want: application.ActivityUnknown, reason: application.SemanticReasonUnsupported},
		{name: "stale", observation: application.HarnessObservation{EventJSON: []byte(`{"type":"turn.started"}`), Process: application.ProcessAlive, ObservedAt: now.Add(-time.Minute), Now: now, FreshnessTTL: 5 * time.Second}, want: application.ActivityUnknown, reason: application.SemanticReasonStale},
		{name: "missing", observation: application.HarnessObservation{Process: application.ProcessUnknown, ObservedAt: now, Now: now}, want: application.ActivityUnknown, reason: application.SemanticReasonMissing},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity := adapter.ClassifySemanticActivity(test.observation)
			if activity.State != test.want || activity.Reason != test.reason {
				t.Fatalf("ClassifySemanticActivity() = %#v, want %q/%q", activity, test.want, test.reason)
			}
		})
	}
}

func probedCodexProfile(t *testing.T, id string) workers.StaticProfile {
	t.Helper()
	source, err := exec.LookPath("printf")
	if err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(canonicalWorkerTempDir(t), "codex")
	if err := os.WriteFile(executable, contents, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := availableCodexProfile(executable, id)
	profile.ExecutableArguments = []string{"codex-cli 0.147.0\n"}
	return profile
}

func codexAttachmentSocket(t *testing.T) (string, func()) {
	t.Helper()
	path := filepath.Join(canonicalWorkerTempDir(t), "attachment.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	return path, func() { _ = listener.Close() }
}

func codexObservation(now time.Time, event string) application.HarnessObservation {
	return application.HarnessObservation{
		EventJSON: []byte(event), Process: application.ProcessAlive,
		ObservedAt: now, Now: now, FreshnessTTL: 5 * time.Second,
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
