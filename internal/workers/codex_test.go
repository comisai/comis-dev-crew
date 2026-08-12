package workers_test

import (
	"context"
	"errors"
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
		VersionArguments: []string{"codex-cli 0.147.0"},
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
		VersionArguments: []string{"codex-cli 0.147.0"},
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
	profile.EnvironmentKeys = append(profile.EnvironmentKeys, "COMIS_EXECUTION_ATTACHMENT_TARGET_NAME")
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
	attachmentTarget := "attachment-0123456789abcdef0123456789abcdef.sock"
	attachmentMount := "/run/comis/attachments/" + attachmentTarget
	request := application.WorkerLaunchRequest{
		ProfileID: profile.ID, Shape: domain.ShapeShip, WorkingDirectory: canonicalWorkerTempDir(t),
		TaskHandle: "task-secret-0001", ManagedRunID: "managed-run-secret-0001",
		WorkspaceLeaseID: "workspace-lease-secret-0001", BriefRevision: 3,
		BriefRevisionHash: strings.Repeat("a", 64),
		Attachment: application.RuntimeSocketAttachment{
			ExecutionAttachmentID: "execution-attachment-secret-0001",
			AttachmentTargetName:  attachmentTarget,
			MountSocketPath:       attachmentMount,
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
		"-c", `model_reasoning_effort="high"`, "--cd", request.WorkingDirectory,
		"Before doing any task work, acknowledge the exact protected launch binding with `devcrew-report acknowledge`. Then read the pinned task brief with `devcrew-report brief`. Use `devcrew-report` for sparse progress, decisions, blocked state, candidate completion, and failure. Treat the protected runtime attachment as the only task/report authority.\n",
	}
	if strings.Join(descriptor.Arguments, "\x00") != strings.Join(wantArguments, "\x00") {
		t.Fatalf("Codex argv = %q, want %q", descriptor.Arguments, wantArguments)
	}
	joined := strings.Join(descriptor.Arguments, "\x00") + string(descriptor.StandardInput)
	for _, secret := range []string{
		request.TaskHandle, request.ManagedRunID, request.WorkspaceLeaseID,
		request.BriefRevisionHash, request.Attachment.ExecutionAttachmentID,
	} {
		if strings.Contains(joined, secret) {
			t.Fatalf("Codex process input leaked task authority %q", secret)
		}
	}
	if len(descriptor.StandardInput) != 0 {
		t.Fatalf("Codex launch must not depend on post-create stdin: %q", descriptor.StandardInput)
	}
	bootstrap := descriptor.Arguments[len(descriptor.Arguments)-1]
	if !strings.Contains(bootstrap, "devcrew-report acknowledge") ||
		!strings.Contains(bootstrap, "devcrew-report brief") ||
		strings.Index(bootstrap, "devcrew-report acknowledge") > strings.Index(bootstrap, "devcrew-report brief") ||
		!containsString(descriptor.EnvironmentKeys, "COMIS_EXECUTION_ATTACHMENT") ||
		!containsString(descriptor.EnvironmentKeys, "COMIS_EXECUTION_ATTACHMENT_TARGET_NAME") ||
		len(descriptor.EnvironmentBindings) != 2 || descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT"] != attachmentMount ||
		descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT_TARGET_NAME"] != attachmentTarget {
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
		{name: "process exited", observation: application.HarnessObservation{Process: application.ProcessExited, ObservedAt: now, Now: now, FreshnessTTL: 5 * time.Second}, want: application.ActivityExited},
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

func TestCodexAdapter_RejectsInvalidConfigurationProbeAndLaunchBoundaries(t *testing.T) {
	profile := availableCodexProfile(codexFixtureExecutable(t), "codex-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{}); err == nil {
		t.Fatal("NewCodexAdapter(empty) error = nil")
	}
	for _, config := range []workers.CodexAdapterConfig{
		{Profiles: catalog, ProfileID: "missing-profile", ExpectedVersion: "codex-cli 0.147.0"},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "0.147.0"},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0", VersionArguments: []string{"bad\narg"}},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0", ProbeEnvironment: map[string]string{"SECRET": "value"}},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0", ProbeEnvironment: map[string]string{"PATH": "bad\x00value"}},
	} {
		if _, err := workers.NewCodexAdapter(config); err == nil {
			t.Fatalf("NewCodexAdapter(%#v) error = nil", config)
		}
	}
	adapter, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.ID() != "codex" {
		t.Fatalf("ID() = %q", adapter.ID())
	}
	//lint:ignore SA1012 The adapter boundary must reject nil before process access.
	if _, err := adapter.ProbeVersion(nil); err == nil {
		t.Fatal("ProbeVersion(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.ProbeVersion(cancelled); err != context.Canceled {
		t.Fatalf("ProbeVersion(cancelled) error = %v", err)
	}
	var unavailable *workers.CodexAdapter
	if _, err := unavailable.ProbeVersion(context.Background()); err == nil {
		t.Fatal("ProbeVersion(unavailable) error = nil")
	}
	probe, err := adapter.ProbeVersion(context.Background())
	if err != nil || probe.Availability != application.HarnessUnknown || probe.Reason != application.HarnessReasonCapabilityUnknown {
		t.Fatalf("ProbeVersion(malformed output) = %#v, %v", probe, err)
	}

	failingProfile := availableCodexProfile(systemExecutable(t, "false"), "codex-failing")
	failingCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{failingProfile})
	if err != nil {
		t.Fatal(err)
	}
	failing, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: failingCatalog, ProfileID: failingProfile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err = failing.ProbeVersion(context.Background())
	if err != nil || probe.Availability != application.HarnessUnavailable || probe.Reason != application.HarnessReasonExecutableUnavailable {
		t.Fatalf("ProbeVersion(failure) = %#v, %v", probe, err)
	}

	attachmentTarget := "attachment-0123456789abcdef0123456789abcdef.sock"
	valid := application.WorkerLaunchRequest{
		ProfileID: profile.ID, Shape: domain.ShapeShip, WorkingDirectory: canonicalWorkerTempDir(t),
		TaskHandle: "task-valid-0001", ManagedRunID: "managed-run-valid-0001",
		WorkspaceLeaseID: "workspace-lease-valid-0001", BriefRevision: 1,
		BriefRevisionHash: strings.Repeat("a", 64),
		Attachment: application.RuntimeSocketAttachment{
			ExecutionAttachmentID: "execution-attachment-valid-0001", AttachmentTargetName: attachmentTarget,
			MountSocketPath: "/run/comis/attachments/" + attachmentTarget,
		},
	}
	//lint:ignore SA1012 The launch boundary must reject nil before profile or mount inspection.
	if _, err := adapter.BuildLaunchDescriptor(nil, valid); err == nil {
		t.Fatal("BuildLaunchDescriptor(nil) error = nil")
	}
	if _, err := adapter.BuildLaunchDescriptor(cancelled, valid); err != context.Canceled {
		t.Fatalf("BuildLaunchDescriptor(cancelled) error = %v", err)
	}
	if _, err := unavailable.BuildLaunchDescriptor(context.Background(), valid); !errors.Is(err, workers.ErrProfileUnknown) {
		t.Fatalf("BuildLaunchDescriptor(unavailable) error = %v", err)
	}
	wrongProfile := valid
	wrongProfile.ProfileID = "missing-profile"
	if _, err := adapter.BuildLaunchDescriptor(context.Background(), wrongProfile); !errors.Is(err, workers.ErrProfileUnknown) {
		t.Fatalf("BuildLaunchDescriptor(wrong profile) error = %v", err)
	}
	for _, mutate := range []func(*application.WorkerLaunchRequest){
		func(request *application.WorkerLaunchRequest) { request.TaskHandle = "../task" },
		func(request *application.WorkerLaunchRequest) { request.ManagedRunID = "bad id" },
		func(request *application.WorkerLaunchRequest) { request.WorkspaceLeaseID = "bad id" },
		func(request *application.WorkerLaunchRequest) { request.BriefRevision = 0 },
		func(request *application.WorkerLaunchRequest) { request.BriefRevisionHash = "bad" },
		func(request *application.WorkerLaunchRequest) { request.Attachment.ExecutionAttachmentID = "bad id" },
		func(request *application.WorkerLaunchRequest) {
			request.Attachment.AttachmentTargetName = "attachment.sock"
		},
		func(request *application.WorkerLaunchRequest) {
			request.Attachment.MountSocketPath = "/run/comis/attachments/attachment-ffffffffffffffffffffffffffffffff.sock"
		},
	} {
		request := valid
		mutate(&request)
		if _, err := adapter.BuildLaunchDescriptor(context.Background(), request); err == nil {
			t.Fatalf("BuildLaunchDescriptor(%#v) error = nil", request)
		}
	}

	noAttachmentProfile := profile
	noAttachmentProfile.ID = "codex-no-attachment"
	noAttachmentProfile.EnvironmentKeys = []string{"PATH"}
	noAttachmentCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{noAttachmentProfile})
	if err != nil {
		t.Fatal(err)
	}
	noAttachment, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: noAttachmentCatalog, ProfileID: noAttachmentProfile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	valid.ProfileID = noAttachmentProfile.ID
	if _, err := noAttachment.BuildLaunchDescriptor(context.Background(), valid); err == nil {
		t.Fatal("BuildLaunchDescriptor(no attachment allowlist) error = nil")
	}

	pathOnlyProfile := profile
	pathOnlyProfile.ID = "codex-path-only"
	pathOnlyCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{pathOnlyProfile})
	if err != nil {
		t.Fatal(err)
	}
	pathOnly, err := workers.NewCodexAdapter(workers.CodexAdapterConfig{
		Profiles: pathOnlyCatalog, ProfileID: pathOnlyProfile.ID, ExpectedVersion: "codex-cli 0.147.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	valid.ProfileID = pathOnlyProfile.ID
	if _, err := pathOnly.BuildLaunchDescriptor(context.Background(), valid); err == nil {
		t.Fatal("BuildLaunchDescriptor(path-only attachment allowlist) error = nil")
	}
}

func probedCodexProfile(t *testing.T, id string) workers.StaticProfile {
	t.Helper()
	source, err := exec.LookPath("echo")
	if err != nil {
		t.Fatal(err)
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	return availableCodexProfile(source, id)
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

func systemExecutable(t *testing.T, name string) string {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatal(err)
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
