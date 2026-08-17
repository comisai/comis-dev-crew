package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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
	maximumClaudeProbeBytes = 8 * 1024
	claudeBootstrapPrompt   = "Before doing any task work, acknowledge the exact protected launch binding with `devcrew-report acknowledge`. Then read the pinned task brief with `devcrew-report brief`. If either command fails, stop without reading or changing the workspace; do not continue task work. Run `devcrew-report --help` before reporting and use only its exact flag syntax; do not invent JSON or stdin formats. Use `devcrew-report` for sparse progress, decisions, blocked state, candidate completion, and failure. Treat the protected runtime attachment as the only task/report authority.\n"
	claudeConfigEnvironment = "CLAUDE_CONFIG_DIR"
)

var claudeVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+ \(Claude Code\)$`)

// ClaudeAdapterConfig pins one exact profile installation, version, and
// owner-private authentication directory.
type ClaudeAdapterConfig struct {
	Profiles             *ProfileCatalog
	ProfileID            string
	ExpectedVersion      string
	VersionArguments     []string
	ProbeEnvironment     map[string]string
	ConfigDirectory      string
	SettleSignalVerified bool
}

// ClaudeAdapter implements the application-owned essential harness boundary.
type ClaudeAdapter struct {
	profiles             *ProfileCatalog
	profileID            string
	expectedVersion      string
	versionArguments     []string
	probeEnvironment     []string
	configDirectory      string
	settleSignalVerified bool
}

// NewClaudeAdapter validates one exact static Claude Code profile and version pin.
func NewClaudeAdapter(config ClaudeAdapterConfig) (*ClaudeAdapter, error) {
	if config.Profiles == nil || !profileIDPattern.MatchString(config.ProfileID) ||
		!claudeVersionPattern.MatchString(config.ExpectedVersion) {
		return nil, errors.New("create Claude adapter: profile catalog, profile ID, and version pin are required")
	}
	profile, found := config.Profiles.profiles[config.ProfileID]
	if !found || profile.Harness != HarnessClaude {
		return nil, errors.New("create Claude adapter: exact Claude profile is unavailable")
	}
	configDirectory, err := validateClaudeConfigDirectory(config.ConfigDirectory)
	if err != nil {
		return nil, err
	}
	versionArguments := append([]string(nil), config.VersionArguments...)
	if len(versionArguments) == 0 {
		versionArguments = []string{"--version"}
	}
	for _, argument := range versionArguments {
		if argument == "" || len([]byte(argument)) > 1024 || strings.ContainsAny(argument, "\x00\r\n") {
			return nil, errors.New("create Claude adapter: version argv is invalid")
		}
	}
	probeEnvironment := make([]string, 0, len(config.ProbeEnvironment)+1)
	probeValues := make(map[string]string, len(config.ProbeEnvironment)+1)
	for key, value := range config.ProbeEnvironment {
		probeValues[key] = value
	}
	probeValues[claudeConfigEnvironment] = configDirectory
	for key, value := range probeValues {
		if !containsEnvironmentKey(profile.EnvironmentKeys, key) || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("create Claude adapter: probe environment is outside the profile allowlist")
		}
		probeEnvironment = append(probeEnvironment, key+"="+value)
	}
	sort.Strings(probeEnvironment)
	return &ClaudeAdapter{
		profiles: config.Profiles, profileID: config.ProfileID, expectedVersion: config.ExpectedVersion,
		versionArguments: versionArguments, probeEnvironment: probeEnvironment,
		configDirectory: configDirectory, settleSignalVerified: config.SettleSignalVerified,
	}, nil
}

// ID identifies the adapter family without leaking installed paths.
func (adapter *ClaudeAdapter) ID() string { return string(HarnessClaude) }

// ProbeVersion executes only the exact pinned executable and entry-point argv.
func (adapter *ClaudeAdapter) ProbeVersion(ctx context.Context) (application.HarnessVersionProbe, error) {
	if ctx == nil {
		return application.HarnessVersionProbe{}, errors.New("probe Claude version: context is required")
	}
	if err := ctx.Err(); err != nil {
		return application.HarnessVersionProbe{}, err
	}
	if adapter == nil || adapter.profiles == nil {
		return application.HarnessVersionProbe{}, errors.New("probe Claude version: adapter is unavailable")
	}
	profile := adapter.profiles.profiles[adapter.profileID]
	arguments := append(append([]string(nil), profile.ExecutableArguments...), adapter.versionArguments...)
	command := exec.CommandContext(ctx, profile.Executable, arguments...)
	command.Env = append([]string(nil), adapter.probeEnvironment...)
	command.WaitDelay = time.Second
	stdout := &claudeBoundedBuffer{limit: maximumClaudeProbeBytes}
	stderr := &claudeBoundedBuffer{limit: maximumClaudeProbeBytes}
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
	if !claudeVersionPattern.MatchString(version) {
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

// BuildLaunchDescriptor creates a fixed Claude Code argv inside the selected
// Comis jail. The generic bootstrap is the positional print-mode prompt so a
// PTY-backed terminal starts atomically; task authority remains in the protected
// runtime attachment.
func (adapter *ClaudeAdapter) BuildLaunchDescriptor(
	ctx context.Context,
	request application.WorkerLaunchRequest,
) (application.WorkerLaunchDescriptor, error) {
	if ctx == nil {
		return application.WorkerLaunchDescriptor{}, errors.New("build Claude launch descriptor: context is required")
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
	if err := validateClaudeLaunchBinding(request); err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	if err := validateClaudeRuntimeAttachment(request.Attachment); err != nil {
		return application.WorkerLaunchDescriptor{}, err
	}
	for _, key := range []string{
		application.RuntimeAttachmentPathEnvironment,
		application.RuntimeAttachmentTargetEnvironment,
		application.RuntimeAttachmentIdentityEnvironment,
		claudeConfigEnvironment,
	} {
		if !containsEnvironmentKey(base.EnvironmentKeys, key) {
			return application.WorkerLaunchDescriptor{}, errors.New("build Claude launch descriptor: required environment key is not allowed")
		}
	}
	arguments := append([]string(nil), base.Arguments...)
	arguments = append(arguments,
		"--input-format", "text", "--output-format", "stream-json", "--verbose",
		"--no-session-persistence", "--safe-mode", "--no-chrome", "--disable-slash-commands",
		"--strict-mcp-config", "--dangerously-skip-permissions", "--permission-mode", "bypassPermissions",
		"--model", base.Model, "--effort", base.Effort, claudeBootstrapPrompt,
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
			application.RuntimeAttachmentPathEnvironment:     request.Attachment.MountSocketPath,
			application.RuntimeAttachmentTargetEnvironment:   request.Attachment.AttachmentTargetName,
			application.RuntimeAttachmentIdentityEnvironment: request.Attachment.RelayIdentity,
			claudeConfigEnvironment:                          adapter.configDirectory,
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

// ClassifySemanticActivity accepts only fresh Claude Code stream-json events
// or a positively attributed process exit.
func (adapter *ClaudeAdapter) ClassifySemanticActivity(observation application.HarnessObservation) application.SemanticActivityResult {
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
	if len(observation.EventJSON) > maximumClaudeProbeBytes {
		return semanticUnknown(application.SemanticReasonMalformed)
	}
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(observation.EventJSON, &event); err != nil || event.Type == "" {
		return semanticUnknown(application.SemanticReasonMalformed)
	}
	switch event.Type {
	case "system", "assistant", "user":
		return application.SemanticActivityResult{State: application.ActivityBusy}
	case "result":
		return semanticUnknown(application.SemanticReasonSettledWithoutReport)
	default:
		return semanticUnknown(application.SemanticReasonUnsupported)
	}
}

func validateClaudeConfigDirectory(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len([]byte(path)) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("create Claude adapter: config directory is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("create Claude adapter: config directory is unavailable or unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.New("create Claude adapter: config directory is not canonical")
	}
	return path, nil
}

func validateClaudeLaunchBinding(request application.WorkerLaunchRequest) error {
	if domain.ValidateTaskHandle(request.TaskHandle) != nil ||
		domain.ValidateAuthorityReference("managedRunId", request.ManagedRunID) != nil ||
		domain.ValidateAuthorityReference("workspaceLeaseId", request.WorkspaceLeaseID) != nil {
		return errors.New("build Claude launch descriptor: task binding is invalid")
	}
	if request.BriefRevision < 1 || domain.ValidateBriefRevisionHash(request.BriefRevisionHash) != nil {
		return errors.New("build Claude launch descriptor: brief binding is invalid")
	}
	return nil
}

func validateClaudeRuntimeAttachment(attachment application.RuntimeSocketAttachment) error {
	if domain.ValidateAuthorityReference("executionAttachmentId", attachment.ExecutionAttachmentID) != nil ||
		domain.ValidateAttachmentTargetName(attachment.AttachmentTargetName) != nil ||
		application.ValidateRuntimeRelayIdentity(attachment.RelayIdentity) != nil {
		return errors.New("build Claude launch descriptor: runtime attachment authority is invalid")
	}
	expectedMount := filepath.Join(application.RuntimeAttachmentMountDirectory, attachment.AttachmentTargetName)
	if attachment.MountSocketPath != expectedMount || filepath.Clean(attachment.MountSocketPath) != attachment.MountSocketPath ||
		strings.ContainsAny(attachment.MountSocketPath, "\x00\r\n") {
		return errors.New("build Claude launch descriptor: runtime attachment mount differs from activation")
	}
	return nil
}

type claudeBoundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (destination *claudeBoundedBuffer) Write(contents []byte) (int, error) {
	remaining := destination.limit - destination.buffer.Len()
	if remaining <= 0 {
		return 0, errors.New("claude probe output exceeded its bound")
	}
	if len(contents) > remaining {
		_, _ = destination.buffer.Write(contents[:remaining])
		return remaining, errors.New("claude probe output exceeded its bound")
	}
	return destination.buffer.Write(contents)
}

var _ application.WorkerHarnessAdapter = (*ClaudeAdapter)(nil)
