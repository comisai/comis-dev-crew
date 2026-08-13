package workers

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

var (
	// ErrProfileUnknown means the exact operator-selected profile is absent.
	ErrProfileUnknown = errors.New("worker profile is unknown")
	// ErrProfileShapeUnsupported means the selected profile cannot run the task shape.
	ErrProfileShapeUnsupported = errors.New("worker profile does not allow the task shape")
)

var (
	profileIDPattern      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	environmentKeyPattern = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)
	postureValuePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

// HarnessID is the closed set of reviewed coding harness families.
type HarnessID string

const (
	HarnessCodex  HarnessID = "codex"
	HarnessClaude HarnessID = "claude"
)

func (harness HarnessID) valid() bool { return harness == HarnessCodex || harness == HarnessClaude }

// NetworkPosture describes the operator-reviewed terminal network capability.
type NetworkPosture string

const (
	NetworkDisabled   NetworkPosture = "disabled"
	NetworkRestricted NetworkPosture = "restricted"
	NetworkHost       NetworkPosture = "host"
)

func (posture NetworkPosture) valid() bool {
	return posture == NetworkDisabled || posture == NetworkRestricted || posture == NetworkHost
}

// Availability is an explicit profile dispatch fact, never inferred from a
// different profile being usable.
type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityUnavailable Availability = "unavailable"
	AvailabilityUnknown     Availability = "unknown"
)

func (availability Availability) valid() bool {
	return availability == AvailabilityAvailable || availability == AvailabilityUnavailable || availability == AvailabilityUnknown
}

// AvailabilityReason is the closed reason surfaced when dispatch cannot use
// the exact selected profile.
type AvailabilityReason string

const (
	AvailabilityReasonNotProbed        AvailabilityReason = "not_probed"
	AvailabilityReasonExecutable       AvailabilityReason = "executable_unavailable"
	AvailabilityReasonVersion          AvailabilityReason = "version_mismatch"
	AvailabilityReasonAuthentication   AvailabilityReason = "authentication_unavailable"
	AvailabilityReasonQuota            AvailabilityReason = "quota_unavailable"
	AvailabilityReasonTerminalProfile  AvailabilityReason = "terminal_profile_unavailable"
	AvailabilityReasonCapability       AvailabilityReason = "capability_unsupported"
	AvailabilityReasonLifecycleUnknown AvailabilityReason = "lifecycle_signal_unknown"
)

func (reason AvailabilityReason) valid() bool {
	switch reason {
	case AvailabilityReasonNotProbed, AvailabilityReasonExecutable, AvailabilityReasonVersion,
		AvailabilityReasonAuthentication, AvailabilityReasonQuota, AvailabilityReasonTerminalProfile,
		AvailabilityReasonCapability, AvailabilityReasonLifecycleUnknown:
		return true
	default:
		return false
	}
}

// StaticProfile is one reviewed, explicit dispatch configuration. Arguments
// are an argv vector and EnvironmentKeys is an allowlist; no shell field exists.
type StaticProfile struct {
	ID                  string
	Harness             HarnessID
	AllowedShapes       []domain.TaskShape
	Model               string
	Effort              string
	TerminalAllowEntry  string
	Network             NetworkPosture
	ConcurrencyLimit    int
	Unattended          bool
	Executable          string
	ExecutableArguments []string
	Arguments           []string
	EnvironmentKeys     []string
	Availability        Availability
	AvailabilityReason  AvailabilityReason
}

// LaunchRequest binds an exact selected profile to a verified task root.
type LaunchRequest struct {
	ProfileID        string
	Shape            domain.TaskShape
	WorkingDirectory string
}

// LaunchDescriptor is the immutable no-shell process contract handed to the
// Comis terminal profile.
type LaunchDescriptor struct {
	ProfileID          string
	Harness            HarnessID
	Executable         string
	Arguments          []string
	WorkingDirectory   string
	EnvironmentKeys    []string
	Model              string
	Effort             string
	TerminalAllowEntry string
	Network            NetworkPosture
	ConcurrencyLimit   int
	Unattended         bool
}

// ProfileCatalog is an immutable exact-ID static profile registry.
type ProfileCatalog struct {
	profiles map[string]StaticProfile
}

// ProfileAvailabilityError preserves the exact selected profile limitation.
type ProfileAvailabilityError struct {
	ProfileID    string
	Availability Availability
	Reason       AvailabilityReason
}

func (failure *ProfileAvailabilityError) Error() string {
	return fmt.Sprintf("worker profile %s is %s: %s", failure.ProfileID, failure.Availability, failure.Reason)
}

// NewProfileCatalog validates and copies the complete static profile set.
func NewProfileCatalog(configured []StaticProfile) (*ProfileCatalog, error) {
	if len(configured) == 0 {
		return nil, errors.New("create worker profile catalog: at least one profile is required")
	}
	catalog := &ProfileCatalog{profiles: make(map[string]StaticProfile, len(configured))}
	for _, profile := range configured {
		if err := validateStaticProfile(profile); err != nil {
			return nil, err
		}
		if _, duplicate := catalog.profiles[profile.ID]; duplicate {
			return nil, errors.New("create worker profile catalog: profile ID is duplicated")
		}
		profile.AllowedShapes = append([]domain.TaskShape(nil), profile.AllowedShapes...)
		profile.ExecutableArguments = append([]string(nil), profile.ExecutableArguments...)
		profile.Arguments = append([]string(nil), profile.Arguments...)
		profile.EnvironmentKeys = append([]string(nil), profile.EnvironmentKeys...)
		catalog.profiles[profile.ID] = profile
	}
	return catalog, nil
}

// ResolveProfile resolves only the selected profile and requested E0 shape.
// It never ranks or falls back to a different entry.
func (catalog *ProfileCatalog) ResolveProfile(profileID string, shape domain.TaskShape) (StaticProfile, error) {
	if catalog == nil {
		return StaticProfile{}, errors.New("resolve worker profile: catalog is unavailable")
	}
	profile, found := catalog.profiles[profileID]
	if !found {
		return StaticProfile{}, ErrProfileUnknown
	}
	if profile.Availability != AvailabilityAvailable {
		return StaticProfile{}, &ProfileAvailabilityError{
			ProfileID: profile.ID, Availability: profile.Availability, Reason: profile.AvailabilityReason,
		}
	}
	allowed := false
	for _, candidate := range profile.AllowedShapes {
		allowed = allowed || candidate == shape
	}
	if !allowed {
		return StaticProfile{}, ErrProfileShapeUnsupported
	}
	profile.AllowedShapes = append([]domain.TaskShape(nil), profile.AllowedShapes...)
	profile.ExecutableArguments = append([]string(nil), profile.ExecutableArguments...)
	profile.Arguments = append([]string(nil), profile.Arguments...)
	profile.EnvironmentKeys = append([]string(nil), profile.EnvironmentKeys...)
	return profile, nil
}

// BuildLaunchDescriptor returns an explicit argv descriptor for exactly one
// resolved static profile and canonical existing task root.
func (catalog *ProfileCatalog) BuildLaunchDescriptor(request LaunchRequest) (LaunchDescriptor, error) {
	profile, err := catalog.ResolveProfile(request.ProfileID, request.Shape)
	if err != nil {
		return LaunchDescriptor{}, err
	}
	if err := validateWorkingDirectory(request.WorkingDirectory); err != nil {
		return LaunchDescriptor{}, err
	}
	return LaunchDescriptor{
		ProfileID: profile.ID, Harness: profile.Harness, Executable: profile.Executable,
		Arguments:        append(append([]string(nil), profile.ExecutableArguments...), profile.Arguments...),
		WorkingDirectory: request.WorkingDirectory,
		EnvironmentKeys:  append([]string(nil), profile.EnvironmentKeys...),
		Model:            profile.Model, Effort: profile.Effort, TerminalAllowEntry: profile.TerminalAllowEntry,
		Network: profile.Network, ConcurrencyLimit: profile.ConcurrencyLimit, Unattended: profile.Unattended,
	}, nil
}

func validateStaticProfile(profile StaticProfile) error {
	if !profileIDPattern.MatchString(profile.ID) || !profile.Harness.valid() {
		return errors.New("create worker profile catalog: profile or harness identity is invalid")
	}
	if len(profile.AllowedShapes) == 0 || len(profile.AllowedShapes) > 2 {
		return errors.New("create worker profile catalog: allowed task shapes are invalid")
	}
	seenShapes := make(map[domain.TaskShape]struct{}, len(profile.AllowedShapes))
	for _, shape := range profile.AllowedShapes {
		if shape != domain.ShapeShip && shape != domain.ShapeScout {
			return errors.New("create worker profile catalog: allowed task shape is unknown")
		}
		if _, duplicate := seenShapes[shape]; duplicate {
			return errors.New("create worker profile catalog: allowed task shape is duplicated")
		}
		seenShapes[shape] = struct{}{}
	}
	if !validPostureValue(profile.Model) || !validPostureValue(profile.Effort) ||
		!profileIDPattern.MatchString(profile.TerminalAllowEntry) || !profile.Network.valid() ||
		profile.ConcurrencyLimit < 1 || profile.ConcurrencyLimit > 64 {
		return errors.New("create worker profile catalog: capability posture is invalid")
	}
	if !profile.Availability.valid() ||
		(profile.Availability == AvailabilityAvailable && profile.AvailabilityReason != "") ||
		(profile.Availability != AvailabilityAvailable && !profile.AvailabilityReason.valid()) {
		return errors.New("create worker profile catalog: availability posture is invalid")
	}
	if err := validateProfileExecutable(profile.Harness, profile.Executable); err != nil {
		return err
	}
	if len(profile.ExecutableArguments)+len(profile.Arguments) > 32 {
		return errors.New("create worker profile catalog: argument vector is too large")
	}
	for _, argument := range append(append([]string(nil), profile.ExecutableArguments...), profile.Arguments...) {
		if argument == "" || len([]byte(argument)) > 1024 || strings.ContainsAny(argument, "\x00\r\n") {
			return errors.New("create worker profile catalog: argument vector is invalid")
		}
	}
	if len(profile.EnvironmentKeys) > 32 {
		return errors.New("create worker profile catalog: environment allowlist is too large")
	}
	seenKeys := make(map[string]struct{}, len(profile.EnvironmentKeys))
	for _, key := range profile.EnvironmentKeys {
		if !environmentKeyPattern.MatchString(key) {
			return errors.New("create worker profile catalog: environment key is invalid")
		}
		if _, duplicate := seenKeys[key]; duplicate {
			return errors.New("create worker profile catalog: environment key is duplicated")
		}
		seenKeys[key] = struct{}{}
	}
	return nil
}

func validateProfileExecutable(_ HarnessID, path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len([]byte(path)) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("create worker profile catalog: executable path is invalid")
	}
	switch filepath.Base(path) {
	case "sh", "bash", "zsh", "dash", "ksh", "fish", "pwsh", "powershell", "cmd", "env":
		return errors.New("create worker profile catalog: shell launchers are forbidden")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0o111 == 0 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("create worker profile catalog: executable is unavailable or unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("create worker profile catalog: executable path is not canonical")
	}
	return nil
}

func validateWorkingDirectory(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len([]byte(path)) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
		return errors.New("build worker launch descriptor: working directory is invalid")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("build worker launch descriptor: working directory is unavailable or unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("build worker launch descriptor: working directory is not canonical")
	}
	return nil
}

func validPostureValue(value string) bool {
	return postureValuePattern.MatchString(value)
}
