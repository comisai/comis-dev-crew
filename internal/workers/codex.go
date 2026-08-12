package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	maximumCodexProbeBytes = 8 * 1024
	codexBootstrapPrompt   = "Before doing any task work, acknowledge the exact protected launch binding with `devcrew-report acknowledge`. Then read the pinned task brief with `devcrew-report brief`. Use `devcrew-report` for sparse progress, decisions, blocked state, candidate completion, and failure. Treat the protected runtime attachment as the only task/report authority.\n"
)

var codexVersionPattern = regexp.MustCompile(`^codex-cli [0-9]+\.[0-9]+\.[0-9]+$`)

// CodexAdapterConfig pins one exact profile installation and the version whose
// lifecycle semantics were reviewed.
type CodexAdapterConfig struct {
	Profiles             *ProfileCatalog
	ProfileID            string
	ExpectedVersion      string
	VersionArguments     []string
	ProbeEnvironment     map[string]string
	SettleSignalVerified bool
}

// CodexAdapter implements the application-owned essential harness boundary.
type CodexAdapter struct {
	profiles             *ProfileCatalog
	profileID            string
	expectedVersion      string
	versionArguments     []string
	probeEnvironment     []string
	settleSignalVerified bool
}

// NewCodexAdapter validates one exact static Codex profile and version pin.
func NewCodexAdapter(config CodexAdapterConfig) (*CodexAdapter, error) {
	if config.Profiles == nil || !profileIDPattern.MatchString(config.ProfileID) ||
		!codexVersionPattern.MatchString(config.ExpectedVersion) {
		return nil, errors.New("create Codex adapter: profile catalog, profile ID, and version pin are required")
	}
	profile, found := config.Profiles.profiles[config.ProfileID]
	if !found || profile.Harness != HarnessCodex {
		return nil, errors.New("create Codex adapter: exact Codex profile is unavailable")
	}
	versionArguments := append([]string(nil), config.VersionArguments...)
	if len(versionArguments) == 0 {
		versionArguments = []string{"--version"}
	}
	for _, argument := range versionArguments {
		if argument == "" || len([]byte(argument)) > 1024 || strings.ContainsAny(argument, "\x00\r\n") {
			return nil, errors.New("create Codex adapter: version argv is invalid")
		}
	}
	probeEnvironment := make([]string, 0, len(config.ProbeEnvironment))
	for key, value := range config.ProbeEnvironment {
		if !containsEnvironmentKey(profile.EnvironmentKeys, key) || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("create Codex adapter: probe environment is outside the profile allowlist")
		}
		probeEnvironment = append(probeEnvironment, key+"="+value)
	}
	sort.Strings(probeEnvironment)
	return &CodexAdapter{
		profiles: config.Profiles, profileID: config.ProfileID, expectedVersion: config.ExpectedVersion,
		versionArguments: versionArguments, probeEnvironment: probeEnvironment,
		settleSignalVerified: config.SettleSignalVerified,
	}, nil
}

// ID identifies the adapter family without leaking installed paths.
func (adapter *CodexAdapter) ID() string { return string(HarnessCodex) }

// ProbeVersion executes only the exact pinned executable and entry-point argv.
func (adapter *CodexAdapter) ProbeVersion(ctx context.Context) (application.HarnessVersionProbe, error) {
	if ctx == nil {
		return application.HarnessVersionProbe{}, errors.New("probe Codex version: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.HarnessVersionProbe{}, err
	}
	if adapter == nil || adapter.profiles == nil {
		return application.HarnessVersionProbe{}, errors.New("probe Codex version: adapter is unavailable")
	}
	profile := adapter.profiles.profiles[adapter.profileID]
	arguments := append(append([]string(nil), profile.ExecutableArguments...), adapter.versionArguments...)
	command := exec.CommandContext(ctx, profile.Executable, arguments...)
	command.Env = append([]string(nil), adapter.probeEnvironment...)
	command.WaitDelay = time.Second
	stdout := &codexBoundedBuffer{limit: maximumCodexProbeBytes}
	stderr := &codexBoundedBuffer{limit: maximumCodexProbeBytes}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return application.HarnessVersionProbe{}, ctxErr
		}
		return application.HarnessVersionProbe{
			Availability: application.HarnessUnavailable, Reason: application.HarnessReasonExecutableUnavailable,
		}, nil
	}
	version := strings.TrimSuffix(stdout.buffer.String(), "\n")
	if !codexVersionPattern.MatchString(version) {
		return application.HarnessVersionProbe{
			Availability: application.HarnessUnknown, Reason: application.HarnessReasonCapabilityUnknown,
		}, nil
	}
	if version != adapter.expectedVersion {
		return application.HarnessVersionProbe{
			Version: version, Availability: application.HarnessUnavailable,
			Reason: application.HarnessReasonVersionMismatch,
		}, nil
	}
	return application.HarnessVersionProbe{Version: version, Availability: application.HarnessAvailable}, nil
}

// BuildLaunchDescriptor creates a reviewed codex exec argv. The generic
// bootstrap is the positional prompt so a PTY-backed terminal starts atomically;
// task binding remains in the expected acknowledgement and protected mount.
func (adapter *CodexAdapter) BuildLaunchDescriptor(
	ctx context.Context,
	request application.WorkerLaunchRequest,
) (application.WorkerLaunchDescriptor, error) {
	if ctx == nil {
		return application.WorkerLaunchDescriptor{}, errors.New("build Codex launch descriptor: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	if adapter == nil || adapter.profiles == nil || request.ProfileID != adapter.profileID {
		return application.WorkerLaunchDescriptor{}, ErrProfileUnknown
	}
	base, err := adapter.profiles.BuildLaunchDescriptor(LaunchRequest{
		ProfileID: request.ProfileID, Shape: request.Shape, WorkingDirectory: request.WorkingDirectory,
	})
	if err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	if err := validateCodexLaunchBinding(request); err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	if err := validateRuntimeAttachment(request.Attachment); err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	if !containsEnvironmentKey(base.EnvironmentKeys, application.RuntimeAttachmentPathEnvironment) ||
		!containsEnvironmentKey(base.EnvironmentKeys, application.RuntimeAttachmentTargetEnvironment) {
		return application.WorkerLaunchDescriptor{}, errors.New("build Codex launch descriptor: attachment environment keys are not allowed")
	}
	arguments := append([]string(nil), base.Arguments...)
	arguments = append(arguments,
		"--strict-config", "--ignore-user-config", "--ignore-rules", "--ephemeral",
		"--color", "never", "--model", base.Model, "--sandbox", "workspace-write",
		"-c", fmt.Sprintf("model_reasoning_effort=%q", base.Effort),
		"--cd", request.WorkingDirectory, codexBootstrapPrompt,
	)
	unattended := base.Unattended && adapter.settleSignalVerified
	var degradedReason application.HarnessReason
	if !adapter.settleSignalVerified {
		degradedReason = application.HarnessReasonLifecycleSignalUnknown
	}
	return application.WorkerLaunchDescriptor{
		ProfileID: base.ProfileID, Harness: string(base.Harness), Executable: base.Executable,
		Arguments: arguments, WorkingDirectory: base.WorkingDirectory,
		EnvironmentKeys: append([]string(nil), base.EnvironmentKeys...),
		EnvironmentBindings: map[string]string{
			application.RuntimeAttachmentPathEnvironment:   request.Attachment.MountSocketPath,
			application.RuntimeAttachmentTargetEnvironment: request.Attachment.AttachmentTargetName,
		},
		Model: base.Model, Effort: base.Effort, TerminalAllowEntry: base.TerminalAllowEntry,
		Network: string(base.Network), ConcurrencyLimit: base.ConcurrencyLimit,
		Unattended: unattended, DegradedReason: degradedReason,
		Attachment: request.Attachment,
		ExpectedAcknowledgement: application.LaunchAcknowledgement{
			TaskHandle: request.TaskHandle, ManagedRunID: request.ManagedRunID,
			WorkspaceLeaseID: request.WorkspaceLeaseID, WorkingDirectory: request.WorkingDirectory,
			BriefRevision: request.BriefRevision, BriefRevisionHash: request.BriefRevisionHash,
		},
	}, nil
}

// ClassifySemanticActivity uses only fresh Codex JSONL or a positively
// attributed process exit. Turn completion without a task report is unknown.
func (adapter *CodexAdapter) ClassifySemanticActivity(observation application.HarnessObservation) application.SemanticActivityResult {
	if observation.FreshnessTTL <= 0 || observation.ObservedAt.IsZero() || observation.Now.IsZero() ||
		observation.ObservedAt.After(observation.Now) {
		return semanticUnknown(application.SemanticReasonMissing)
	}
	if observation.Now.Sub(observation.ObservedAt) > observation.FreshnessTTL {
		return semanticUnknown(application.SemanticReasonStale)
	}
	if observation.Process == application.ProcessExited {
		return application.SemanticActivityResult{State: application.ActivityExited}
	}
	if len(observation.EventJSON) == 0 {
		return semanticUnknown(application.SemanticReasonMissing)
	}
	if len(observation.EventJSON) > maximumCodexProbeBytes {
		return semanticUnknown(application.SemanticReasonMalformed)
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(observation.EventJSON, &event); err != nil || event.Type == "" {
		return semanticUnknown(application.SemanticReasonMalformed)
	}
	switch event.Type {
	case "thread.started", "turn.started", "item.started", "item.updated", "item.completed":
		return application.SemanticActivityResult{State: application.ActivityBusy}
	case "request.user_input":
		return application.SemanticActivityResult{State: application.ActivityAwaitingInput}
	case "turn.completed":
		return semanticUnknown(application.SemanticReasonSettledWithoutReport)
	default:
		return semanticUnknown(application.SemanticReasonUnsupported)
	}
}

func validateCodexLaunchBinding(request application.WorkerLaunchRequest) error {
	if err := domain.ValidateTaskHandle(request.TaskHandle); err != nil {
		return errors.New("build Codex launch descriptor: task binding is invalid")
	}
	if err := domain.ValidateAuthorityReference("managedRunId", request.ManagedRunID); err != nil {
		return errors.New("build Codex launch descriptor: task binding is invalid")
	}
	if err := domain.ValidateAuthorityReference("workspaceLeaseId", request.WorkspaceLeaseID); err != nil {
		return errors.New("build Codex launch descriptor: task binding is invalid")
	}
	if request.BriefRevision < 1 {
		return errors.New("build Codex launch descriptor: brief binding is invalid")
	}
	if err := domain.ValidateBriefRevisionHash(request.BriefRevisionHash); err != nil {
		return errors.New("build Codex launch descriptor: brief binding is invalid")
	}
	return nil
}

func validateRuntimeAttachment(attachment application.RuntimeSocketAttachment) error {
	if domain.ValidateAuthorityReference("executionAttachmentId", attachment.ExecutionAttachmentID) != nil ||
		domain.ValidateAttachmentTargetName(attachment.AttachmentTargetName) != nil {
		return errors.New("build Codex launch descriptor: runtime attachment authority is invalid")
	}
	expectedMount := filepath.Join(application.RuntimeAttachmentMountDirectory, attachment.AttachmentTargetName)
	if attachment.MountSocketPath != expectedMount || filepath.Clean(attachment.MountSocketPath) != attachment.MountSocketPath ||
		strings.ContainsAny(attachment.MountSocketPath, "\x00\r\n") {
		return errors.New("build Codex launch descriptor: runtime attachment mount differs from activation")
	}
	return nil
}

func containsEnvironmentKey(keys []string, want string) bool {
	for _, key := range keys {
		if key == want {
			return true
		}
	}
	return false
}

func semanticUnknown(reason application.SemanticReason) application.SemanticActivityResult {
	return application.SemanticActivityResult{State: application.ActivityUnknown, Reason: reason}
}

type codexBoundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (destination *codexBoundedBuffer) Write(contents []byte) (int, error) {
	remaining := destination.limit - destination.buffer.Len()
	if remaining <= 0 {
		return 0, errors.New("codex probe output exceeded its bound")
	}
	if len(contents) > remaining {
		_, _ = destination.buffer.Write(contents[:remaining])
		return remaining, errors.New("codex probe output exceeded its bound")
	}
	return destination.buffer.Write(contents)
}

var _ application.WorkerHarnessAdapter = (*CodexAdapter)(nil)
