// Package validation resolves reviewed task validation profiles and runs their
// fixed programs without a shell or worker-selected arguments.
package validation

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	maximumCheckTimeout = 2 * time.Hour
	maximumEvidenceTTL  = 30 * 24 * time.Hour
)

var (
	identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,63}$`)
	revisionPattern   = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
	literalPattern    = regexp.MustCompile(`^[A-Za-z0-9_./:=,+@%~-]+$`)
	mediaTypePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,63}/[a-z0-9][a-z0-9.+-]{0,63}$`)
)

// ArgumentKind is the closed reviewed validation-argument vocabulary.
type ArgumentKind string

const (
	ArgumentLiteral   ArgumentKind = "literal"
	ArgumentTaskField ArgumentKind = "task_field"
)

// TaskField names one service-owned task fact that may enter a fixed argument.
type TaskField string

const (
	FieldTaskHandle   TaskField = "task_handle"
	FieldWorktreePath TaskField = "worktree_path"
	FieldBaseRevision TaskField = "base_revision"
	FieldHeadRevision TaskField = "head_revision"
)

// ArtifactKind is the closed E0 output-artifact rule vocabulary.
type ArtifactKind string

const ArtifactRegularFile ArtifactKind = "regular_file"

// Program maps one opaque reviewed identity to an absolute executable.
type Program struct {
	ID         string
	Executable string
}

// ArgumentTemplate is either a reviewed literal or one closed service field.
type ArgumentTemplate struct {
	Kind  ArgumentKind
	Value string
}

// LocalCheck is one fixed program invocation in a validation profile.
type LocalCheck struct {
	ID        string
	ProgramID string
	Arguments []ArgumentTemplate
	Timeout   time.Duration
	Required  bool
}

// ForgeCheck identifies one required check name observed through forge truth.
type ForgeCheck struct {
	Name     string
	Required bool
}

// ArtifactRule bounds one immutable task artifact.
type ArtifactRule struct {
	Kind         ArtifactKind
	RelativePath string
	MediaType    string
	MaxBytes     int64
}

// Profile is one complete operator-reviewed validation policy.
type Profile struct {
	ID            string
	LocalChecks   []LocalCheck
	ForgeChecks   []ForgeCheck
	ArtifactRules []ArtifactRule
	EvidenceTTL   time.Duration
}

// CatalogConfig is the complete immutable profile/program input.
type CatalogConfig struct {
	Programs []Program
	Profiles []Profile
}

// TaskFields are server-owned values used to resolve typed templates.
type TaskFields struct {
	TaskHandle   string
	WorktreePath string
	BaseRevision string
	HeadRevision string
}

// Command is one fully resolved fixed validation invocation.
type Command struct {
	ProgramID        string
	Executable       string
	Arguments        []string
	WorkingDirectory string
	Timeout          time.Duration
	Required         bool
}

// Catalog stores immutable reviewed profiles indexed by opaque identity.
type Catalog struct {
	programs map[string]Program
	profiles map[string]Profile
}

// NewCatalog validates and freezes the complete validation configuration.
func NewCatalog(config CatalogConfig) (*Catalog, error) {
	if len(config.Programs) == 0 || len(config.Profiles) == 0 {
		return nil, errors.New("create validation catalog: programs and profiles are required")
	}
	programs := make(map[string]Program, len(config.Programs))
	for _, program := range config.Programs {
		if !identifierPattern.MatchString(program.ID) || !filepath.IsAbs(program.Executable) ||
			filepath.Clean(program.Executable) != program.Executable || isShellExecutable(program.Executable) {
			return nil, errors.New("create validation catalog: reviewed program is invalid")
		}
		if _, exists := programs[program.ID]; exists {
			return nil, errors.New("create validation catalog: program identity is duplicated")
		}
		programs[program.ID] = program
	}
	profiles := make(map[string]Profile, len(config.Profiles))
	for _, profile := range config.Profiles {
		if err := validateProfile(profile, programs); err != nil {
			return nil, err
		}
		if _, exists := profiles[profile.ID]; exists {
			return nil, errors.New("create validation catalog: profile identity is duplicated")
		}
		profiles[profile.ID] = cloneProfile(profile)
	}
	return &Catalog{programs: programs, profiles: profiles}, nil
}

func validateProfile(profile Profile, programs map[string]Program) error {
	if !identifierPattern.MatchString(profile.ID) || len(profile.LocalChecks) == 0 ||
		profile.EvidenceTTL <= 0 || profile.EvidenceTTL > maximumEvidenceTTL {
		return errors.New("create validation catalog: profile is invalid")
	}
	checks := make(map[string]struct{}, len(profile.LocalChecks))
	for _, check := range profile.LocalChecks {
		if !identifierPattern.MatchString(check.ID) || check.Timeout <= 0 || check.Timeout > maximumCheckTimeout ||
			len(check.Arguments) == 0 {
			return errors.New("create validation catalog: local check is invalid")
		}
		if _, exists := programs[check.ProgramID]; !exists {
			return errors.New("create validation catalog: local check program is unavailable")
		}
		if _, exists := checks[check.ID]; exists {
			return errors.New("create validation catalog: local check identity is duplicated")
		}
		checks[check.ID] = struct{}{}
		for _, argument := range check.Arguments {
			if !validArgumentTemplate(argument) {
				return errors.New("create validation catalog: argument template is invalid")
			}
		}
	}
	forgeNames := make(map[string]struct{}, len(profile.ForgeChecks))
	for _, check := range profile.ForgeChecks {
		if check.Name == "" || len(check.Name) > 128 || strings.TrimSpace(check.Name) != check.Name || strings.ContainsAny(check.Name, "\x00\r\n") {
			return errors.New("create validation catalog: forge check is invalid")
		}
		if _, exists := forgeNames[check.Name]; exists {
			return errors.New("create validation catalog: forge check is duplicated")
		}
		forgeNames[check.Name] = struct{}{}
	}
	if len(profile.ArtifactRules) > 1 {
		return errors.New("create validation catalog: artifact rules are ambiguous")
	}
	for _, rule := range profile.ArtifactRules {
		if rule.Kind != ArtifactRegularFile || rule.MaxBytes <= 0 || rule.MaxBytes > 1<<30 ||
			!validArtifactPath(rule.RelativePath) || !mediaTypePattern.MatchString(rule.MediaType) {
			return errors.New("create validation catalog: artifact rule is invalid")
		}
	}
	return nil
}

func validArtifactPath(path string) bool {
	if path == "" || len(path) > 256 || filepath.IsAbs(path) || filepath.Clean(path) != path ||
		path == "." || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	for _, component := range strings.Split(path, string(filepath.Separator)) {
		if component == ".." || component == "" {
			return false
		}
	}
	return true
}

func validArgumentTemplate(argument ArgumentTemplate) bool {
	switch argument.Kind {
	case ArgumentLiteral:
		return argument.Value != "" && len(argument.Value) <= 1024 && literalPattern.MatchString(argument.Value)
	case ArgumentTaskField:
		switch TaskField(argument.Value) {
		case FieldTaskHandle, FieldWorktreePath, FieldBaseRevision, FieldHeadRevision:
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func isShellExecutable(executable string) bool {
	switch filepath.Base(executable) {
	case "sh", "bash", "dash", "zsh", "ksh", "fish":
		return true
	default:
		return false
	}
}

// ResolveProfile returns an isolated copy of one reviewed profile.
func (catalog *Catalog) ResolveProfile(profileID string) (Profile, error) {
	if catalog == nil {
		return Profile{}, errors.New("resolve validation profile: catalog is unavailable")
	}
	profile, exists := catalog.profiles[profileID]
	if !exists {
		return Profile{}, errors.New("resolve validation profile: profile is unavailable")
	}
	return cloneProfile(profile), nil
}

// ResolveLocalCheck fills typed server fields without accepting free-form arguments.
func (catalog *Catalog) ResolveLocalCheck(profileID, checkID string, fields TaskFields) (Command, error) {
	profile, err := catalog.ResolveProfile(profileID)
	if err != nil {
		return Command{}, err
	}
	if err := validateTaskFields(fields); err != nil {
		return Command{}, err
	}
	for _, check := range profile.LocalChecks {
		if check.ID != checkID {
			continue
		}
		program, exists := catalog.programs[check.ProgramID]
		if !exists {
			return Command{}, errors.New("resolve validation check: program is unavailable")
		}
		arguments := make([]string, 0, len(check.Arguments))
		for _, template := range check.Arguments {
			value, resolveErr := resolveArgument(template, fields)
			if resolveErr != nil {
				return Command{}, resolveErr
			}
			arguments = append(arguments, value)
		}
		return Command{
			ProgramID: check.ProgramID, Executable: program.Executable,
			Arguments: arguments, WorkingDirectory: fields.WorktreePath,
			Timeout: check.Timeout, Required: check.Required,
		}, nil
	}
	return Command{}, errors.New("resolve validation check: check is unavailable")
}

func validateTaskFields(fields TaskFields) error {
	if !identifierPattern.MatchString(fields.TaskHandle) || !filepath.IsAbs(fields.WorktreePath) ||
		filepath.Clean(fields.WorktreePath) != fields.WorktreePath || !revisionPattern.MatchString(fields.BaseRevision) ||
		!revisionPattern.MatchString(fields.HeadRevision) {
		return errors.New("resolve validation check: task fields are invalid")
	}
	return nil
}

func resolveArgument(template ArgumentTemplate, fields TaskFields) (string, error) {
	if template.Kind == ArgumentLiteral {
		return template.Value, nil
	}
	switch TaskField(template.Value) {
	case FieldTaskHandle:
		return fields.TaskHandle, nil
	case FieldWorktreePath:
		return fields.WorktreePath, nil
	case FieldBaseRevision:
		return fields.BaseRevision, nil
	case FieldHeadRevision:
		return fields.HeadRevision, nil
	default:
		return "", errors.New("resolve validation check: task field is unavailable")
	}
}

func cloneProfile(profile Profile) Profile {
	cloned := profile
	cloned.LocalChecks = append([]LocalCheck(nil), profile.LocalChecks...)
	for index := range cloned.LocalChecks {
		cloned.LocalChecks[index].Arguments = append([]ArgumentTemplate(nil), profile.LocalChecks[index].Arguments...)
	}
	cloned.ForgeChecks = append([]ForgeCheck(nil), profile.ForgeChecks...)
	cloned.ArtifactRules = append([]ArtifactRule(nil), profile.ArtifactRules...)
	return cloned
}
