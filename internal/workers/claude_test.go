package workers_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

func TestClaudeAdapterProbesExactInstalledArtifact(t *testing.T) {
	profile := probedClaudeProfile(t, "claude-probed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	configDirectory := claudeConfigDirectory(t)
	adapter, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)",
		VersionArguments: []string{"2.1.224 (Claude Code)"}, ConfigDirectory: configDirectory,
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err := adapter.ProbeVersion(context.Background())
	if err != nil || probe.Version != "2.1.224 (Claude Code)" ||
		probe.Availability != application.HarnessAvailable || probe.Reason != "" {
		t.Fatalf("ProbeVersion() = %#v, %v", probe, err)
	}

	mismatched, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.225 (Claude Code)",
		VersionArguments: []string{"2.1.224 (Claude Code)"}, ConfigDirectory: configDirectory,
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

func TestClaudeAdapterBuildsConfinedProtectedLaunchWithoutAuthorityLeak(t *testing.T) {
	profile := availableClaudeProfile(codexFixtureExecutable(t), "claude-reviewed")
	profile.Unattended = true
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	configDirectory := claudeConfigDirectory(t)
	adapter, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)",
		ConfigDirectory: configDirectory, SettleSignalVerified: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := validClaudeLaunchRequest(t, profile.ID)
	descriptor, err := adapter.BuildLaunchDescriptor(context.Background(), request)
	if err != nil {
		t.Fatalf("BuildLaunchDescriptor() error = %v", err)
	}
	wantArguments := []string{
		"-p", "--input-format", "text", "--output-format", "stream-json", "--verbose",
		"--no-session-persistence", "--safe-mode", "--no-chrome", "--disable-slash-commands",
		"--strict-mcp-config", "--dangerously-skip-permissions", "--permission-mode", "bypassPermissions",
		"--model", "claude-opus-4-6", "--effort", "high",
		"Before doing any task work, acknowledge the exact protected launch binding with `devcrew-report acknowledge`. " +
			"Then read the pinned task brief with `devcrew-report brief`. Use `devcrew-report` for sparse progress, " +
			"decisions, blocked state, candidate completion, and failure. Treat the protected runtime attachment as " +
			"the only task/report authority.\n",
	}
	if strings.Join(descriptor.Arguments, "\x00") != strings.Join(wantArguments, "\x00") ||
		len(descriptor.StandardInput) != 0 ||
		descriptor.Harness != "claude" || descriptor.Unattended ||
		descriptor.DegradedReason != application.HarnessReasonLifecycleSignalUnknown {
		t.Fatalf("Claude launch descriptor = %#v", descriptor)
	}
	joined := strings.Join(descriptor.Arguments, "\x00") + string(descriptor.StandardInput)
	for _, authority := range []string{
		request.TaskHandle, request.ManagedRunID, request.WorkspaceLeaseID,
		request.BriefRevisionHash, request.Attachment.ExecutionAttachmentID,
	} {
		if strings.Contains(joined, authority) {
			t.Fatalf("Claude process input leaked task authority %q", authority)
		}
	}
	if descriptor.EnvironmentBindings["CLAUDE_CONFIG_DIR"] != configDirectory ||
		descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT"] != request.Attachment.MountSocketPath ||
		descriptor.EnvironmentBindings["COMIS_EXECUTION_ATTACHMENT_TARGET_NAME"] != request.Attachment.AttachmentTargetName ||
		len(descriptor.EnvironmentBindings) != 3 ||
		!strings.Contains(descriptor.Arguments[len(descriptor.Arguments)-1], "devcrew-report acknowledge") ||
		!strings.Contains(descriptor.Arguments[len(descriptor.Arguments)-1], "devcrew-report brief") {
		t.Fatalf("Claude protected launch bindings = %#v", descriptor)
	}
	if descriptor.ExpectedAcknowledgement.TaskHandle != request.TaskHandle ||
		descriptor.ExpectedAcknowledgement.ManagedRunID != request.ManagedRunID ||
		descriptor.ExpectedAcknowledgement.WorkspaceLeaseID != request.WorkspaceLeaseID ||
		descriptor.ExpectedAcknowledgement.BriefRevisionHash != request.BriefRevisionHash {
		t.Fatalf("Claude expected acknowledgement = %#v", descriptor.ExpectedAcknowledgement)
	}
}

func TestClaudeAdapterClassifiesOnlyFreshStreamEvents(t *testing.T) {
	profile := availableClaudeProfile(codexFixtureExecutable(t), "claude-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)",
		ConfigDirectory: claudeConfigDirectory(t), SettleSignalVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 12, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		observation application.HarnessObservation
		want        application.SemanticActivity
		reason      application.SemanticReason
	}{
		{name: "system initialization", observation: claudeObservation(now, `{"type":"system","subtype":"init"}`), want: application.ActivityBusy},
		{name: "assistant output", observation: claudeObservation(now, `{"type":"assistant"}`), want: application.ActivityBusy},
		{name: "tool result input", observation: claudeObservation(now, `{"type":"user"}`), want: application.ActivityBusy},
		{name: "result without report", observation: claudeObservation(now, `{"type":"result"}`), want: application.ActivityUnknown, reason: application.SemanticReasonSettledWithoutReport},
		{name: "process exited", observation: application.HarnessObservation{Process: application.ProcessExited, ObservedAt: now, Now: now, FreshnessTTL: 5 * time.Second}, want: application.ActivityExited},
		{name: "malformed", observation: claudeObservation(now, `{`), want: application.ActivityUnknown, reason: application.SemanticReasonMalformed},
		{name: "unsupported", observation: claudeObservation(now, `{"type":"invented"}`), want: application.ActivityUnknown, reason: application.SemanticReasonUnsupported},
		{name: "stale", observation: application.HarnessObservation{EventJSON: []byte(`{"type":"assistant"}`), Process: application.ProcessAlive, ObservedAt: now.Add(-time.Minute), Now: now, FreshnessTTL: 5 * time.Second}, want: application.ActivityUnknown, reason: application.SemanticReasonStale},
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

func TestClaudeAdapterRejectsUnsafeConfigurationAndLaunchAuthority(t *testing.T) {
	profile := availableClaudeProfile(codexFixtureExecutable(t), "claude-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	configDirectory := claudeConfigDirectory(t)
	if _, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{}); err == nil {
		t.Fatal("NewClaudeAdapter(empty) error = nil")
	}
	unsafeDirectory := claudeConfigDirectory(t)
	if err := os.Chmod(unsafeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, config := range []workers.ClaudeAdapterConfig{
		{Profiles: catalog, ProfileID: "missing-profile", ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: configDirectory},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "claude 2.1.224", ConfigDirectory: configDirectory},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: unsafeDirectory},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: configDirectory, VersionArguments: []string{"bad\narg"}},
		{Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: configDirectory, ProbeEnvironment: map[string]string{"SECRET": "value"}},
	} {
		if _, err := workers.NewClaudeAdapter(config); err == nil {
			t.Fatalf("NewClaudeAdapter(%#v) error = nil", config)
		}
	}
	adapter, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: configDirectory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if adapter.ID() != "claude" {
		t.Fatalf("ID() = %q", adapter.ID())
	}
	//lint:ignore SA1012 The probe boundary must reject nil before process access.
	if _, err := adapter.ProbeVersion(nil); err == nil {
		t.Fatal("ProbeVersion(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.ProbeVersion(cancelled); err != context.Canceled {
		t.Fatalf("ProbeVersion(cancelled) error = %v", err)
	}
	var unavailable *workers.ClaudeAdapter
	if _, err := unavailable.ProbeVersion(context.Background()); err == nil {
		t.Fatal("ProbeVersion(unavailable) error = nil")
	}
	probe, err := adapter.ProbeVersion(context.Background())
	if err != nil || probe.Availability != application.HarnessUnknown ||
		probe.Reason != application.HarnessReasonCapabilityUnknown {
		t.Fatalf("ProbeVersion(malformed output) = %#v, %v", probe, err)
	}
	failingProfile := availableClaudeProfile(systemExecutable(t, "false"), "claude-failing")
	failingCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{failingProfile})
	if err != nil {
		t.Fatal(err)
	}
	failing, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: failingCatalog, ProfileID: failingProfile.ID, ExpectedVersion: "2.1.224 (Claude Code)",
		ConfigDirectory: configDirectory,
	})
	if err != nil {
		t.Fatal(err)
	}
	probe, err = failing.ProbeVersion(context.Background())
	if err != nil || probe.Availability != application.HarnessUnavailable ||
		probe.Reason != application.HarnessReasonExecutableUnavailable {
		t.Fatalf("ProbeVersion(failure) = %#v, %v", probe, err)
	}
	request := validClaudeLaunchRequest(t, profile.ID)
	//lint:ignore SA1012 The launch boundary must reject nil before profile or mount inspection.
	if _, err := adapter.BuildLaunchDescriptor(nil, request); err == nil {
		t.Fatal("BuildLaunchDescriptor(nil) error = nil")
	}
	if _, err := adapter.BuildLaunchDescriptor(cancelled, request); err != context.Canceled {
		t.Fatalf("BuildLaunchDescriptor(cancelled) error = %v", err)
	}
	if _, err := unavailable.BuildLaunchDescriptor(context.Background(), request); !errors.Is(err, workers.ErrProfileUnknown) {
		t.Fatalf("BuildLaunchDescriptor(unavailable) error = %v", err)
	}
	for _, mutate := range []func(*application.WorkerLaunchRequest){
		func(value *application.WorkerLaunchRequest) { value.TaskHandle = "../task" },
		func(value *application.WorkerLaunchRequest) { value.ManagedRunID = "bad id" },
		func(value *application.WorkerLaunchRequest) { value.WorkspaceLeaseID = "bad id" },
		func(value *application.WorkerLaunchRequest) { value.BriefRevision = 0 },
		func(value *application.WorkerLaunchRequest) { value.BriefRevisionHash = "bad" },
		func(value *application.WorkerLaunchRequest) { value.Attachment.ExecutionAttachmentID = "bad id" },
		func(value *application.WorkerLaunchRequest) {
			value.Attachment.AttachmentTargetName = "attachment.sock"
		},
		func(value *application.WorkerLaunchRequest) {
			value.Attachment.MountSocketPath = "/run/comis/attachments/other.sock"
		},
	} {
		invalid := request
		mutate(&invalid)
		if _, err := adapter.BuildLaunchDescriptor(context.Background(), invalid); err == nil {
			t.Fatalf("BuildLaunchDescriptor(%#v) error = nil", invalid)
		}
	}
	missingEnvironment := profile
	missingEnvironment.ID = "claude-missing-environment"
	missingEnvironment.EnvironmentKeys = []string{"COMIS_EXECUTION_ATTACHMENT", "CLAUDE_CONFIG_DIR", "PATH"}
	missingEnvironmentCatalog, err := workers.NewProfileCatalog([]workers.StaticProfile{missingEnvironment})
	if err != nil {
		t.Fatal(err)
	}
	missingEnvironmentAdapter, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: missingEnvironmentCatalog, ProfileID: missingEnvironment.ID,
		ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: configDirectory,
	})
	if err != nil {
		t.Fatal(err)
	}
	request.ProfileID = missingEnvironment.ID
	if _, err := missingEnvironmentAdapter.BuildLaunchDescriptor(context.Background(), request); err == nil {
		t.Fatal("BuildLaunchDescriptor(missing environment) error = nil")
	}
}

func availableClaudeProfile(executable, id string) workers.StaticProfile {
	return workers.StaticProfile{
		ID: id, Harness: workers.HarnessClaude, AllowedShapes: []domain.TaskShape{domain.ShapeShip, domain.ShapeScout},
		Model: "claude-opus-4-6", Effort: "high", TerminalAllowEntry: "claude-confined",
		Network: workers.NetworkRestricted, ConcurrencyLimit: 2, Executable: executable,
		Arguments:       []string{"-p"},
		EnvironmentKeys: []string{"COMIS_EXECUTION_ATTACHMENT", "COMIS_EXECUTION_ATTACHMENT_TARGET_NAME", "CLAUDE_CONFIG_DIR", "PATH"},
		Availability:    workers.AvailabilityAvailable,
	}
}

func probedClaudeProfile(t *testing.T, id string) workers.StaticProfile {
	t.Helper()
	profile := probedCodexProfile(t, id)
	profile.Harness = workers.HarnessClaude
	profile.Model = "claude-opus-4-6"
	profile.TerminalAllowEntry = "claude-confined"
	profile.Arguments = []string{"-p"}
	profile.EnvironmentKeys = []string{"COMIS_EXECUTION_ATTACHMENT", "COMIS_EXECUTION_ATTACHMENT_TARGET_NAME", "CLAUDE_CONFIG_DIR", "PATH"}
	return profile
}

func claudeConfigDirectory(t *testing.T) string {
	t.Helper()
	path := canonicalWorkerTempDir(t)
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func validClaudeLaunchRequest(t *testing.T, profileID string) application.WorkerLaunchRequest {
	t.Helper()
	attachmentTarget := "attachment-0123456789abcdef0123456789abcdef.sock"
	return application.WorkerLaunchRequest{
		ProfileID: profileID, Shape: domain.ShapeShip, WorkingDirectory: canonicalWorkerTempDir(t),
		TaskHandle: "task-secret-0001", ManagedRunID: "managed-run-secret-0001",
		WorkspaceLeaseID: "workspace-lease-secret-0001", BriefRevision: 3,
		BriefRevisionHash: strings.Repeat("a", 64),
		Attachment: application.RuntimeSocketAttachment{
			ExecutionAttachmentID: "execution-attachment-secret-0001",
			AttachmentTargetName:  attachmentTarget,
			MountSocketPath:       "/run/comis/attachments/" + attachmentTarget,
		},
	}
}

func claudeObservation(now time.Time, event string) application.HarnessObservation {
	return application.HarnessObservation{
		EventJSON: []byte(event), Process: application.ProcessAlive,
		ObservedAt: now, Now: now, FreshnessTTL: 5 * time.Second,
	}
}

func TestProfileCatalogAcceptsOnlyReviewedClaudeHarnessIdentity(t *testing.T) {
	profile := availableClaudeProfile(codexFixtureExecutable(t), "claude-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatalf("NewProfileCatalog(Claude) error = %v", err)
	}
	resolved, err := catalog.ResolveProfile(profile.ID, domain.ShapeScout)
	if err != nil || resolved.Harness != workers.HarnessClaude {
		t.Fatalf("ResolveProfile(Claude) = %#v, %v", resolved, err)
	}
}

func TestClaudeAdapterRejectsSymlinkedConfigDirectory(t *testing.T) {
	profile := availableClaudeProfile(codexFixtureExecutable(t), "claude-reviewed")
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{profile})
	if err != nil {
		t.Fatal(err)
	}
	target := claudeConfigDirectory(t)
	link := filepath.Join(canonicalWorkerTempDir(t), "claude-config")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := workers.NewClaudeAdapter(workers.ClaudeAdapterConfig{
		Profiles: catalog, ProfileID: profile.ID, ExpectedVersion: "2.1.224 (Claude Code)", ConfigDirectory: link,
	}); err == nil {
		t.Fatal("NewClaudeAdapter(symlinked config directory) error = nil")
	}
}
