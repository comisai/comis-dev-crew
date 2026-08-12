package service

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/workers"
)

const serviceUsage = `Usage: devcrew-service [--database PATH] [--socket PATH]
       devcrew-service --database PATH --socket PATH --mcp-socket PATH --runtime-root PATH
         --service-instance ID --git-executable PATH --approved-root PATH
         --repository-id ID --repository-primary PATH --worktree-root PATH
         --repository-default-branch BRANCH
         --comis-socket PATH --comis-credential-file PATH
         --comis-handshake-operation ID --codex-profile ID --codex-executable PATH
         --codex-version VERSION --codex-model MODEL --codex-effort EFFORT
		 --codex-terminal-allow-entry ID --codex-network POSTURE --codex-concurrency N
		 [--claude-profile ID --claude-executable PATH --claude-version VERSION
		  --claude-model MODEL --claude-effort EFFORT --claude-terminal-allow-entry ID
		  --claude-network POSTURE --claude-concurrency N --claude-config-directory PATH]
		 --candidate-config PATH [--fixture-worker --fixture-decision TEXT --fixture-artifact FILE]

Run the sole durable comis-dev-crew service authority.

Options:
  --database PATH                 Owner-private SQLite database path
  --socket PATH                   Owner-only operator Unix socket path
  --mcp-socket PATH               Owner-only MCP facade Unix socket path
  --runtime-root PATH             Owner-only per-task attachment root
  --service-instance ID           Exact Comis capability-service instance identity
  --git-executable PATH           Absolute Git executable path
  --approved-root PATH            Root containing configured repository paths
  --repository-id ID              Opaque configured repository identity
  --repository-primary PATH       Canonical primary checkout path
  --worktree-root PATH            Canonical dedicated worktree parent
  --repository-default-branch BRANCH  Configured local default branch
  --comis-socket PATH             Owner-only Comis control Unix socket
  --comis-credential-file PATH    Owner-private Comis bearer file
  --comis-handshake-operation ID  Stable handshake operation identity
  --preparation-ttl DURATION      Activation preparation lifetime (default 10m)
  --codex-profile ID              Exact reviewed worker profile identity
  --codex-executable PATH         Canonical Codex executable path
  --codex-version VERSION         Exact reviewed codex-cli version output
  --codex-model MODEL             Pinned Codex model
  --codex-effort EFFORT           Pinned reasoning effort
  --codex-terminal-allow-entry ID Reviewed Comis terminal allow-entry identity
  --codex-network POSTURE         disabled, restricted, or host
  --codex-concurrency N           Reviewed profile concurrency limit
  --claude-profile ID             Exact reviewed Claude Code profile identity
  --claude-executable PATH        Canonical Claude Code executable path
  --claude-version VERSION        Exact reviewed Claude Code version output
  --claude-model MODEL            Pinned Claude model
  --claude-effort EFFORT          Pinned Claude reasoning effort
  --claude-terminal-allow-entry ID  Reviewed Comis terminal allow-entry identity
  --claude-network POSTURE        disabled, restricted, or host
  --claude-concurrency N          Reviewed profile concurrency limit
  --claude-config-directory PATH  Owner-private Claude Code config directory
  --candidate-config PATH         Owner-private validation and forge policy
  --fixture-worker                Enable the deterministic in-process worker
  --fixture-decision TEXT         Fixed deterministic worker decision response
  --fixture-artifact FILE         Single deterministic candidate artifact filename
  --help, -h                      Show this help
  --version                       Show version
`

// ServiceRunner is the injectable daemon lifecycle used by the command adapter.
type ServiceRunner func(context.Context, Config) error

// CommandConfig supplies host defaults and the version identity.
type CommandConfig struct {
	DefaultDatabasePath string
	DefaultSocketPath   string
	Version             string
	RunService          ServiceRunner
}

// RunCommand parses the strict daemon command surface and returns a process exit code.
func RunCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config CommandConfig) int {
	flags := flag.NewFlagSet("devcrew-service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databasePath := config.DefaultDatabasePath
	socketPath := config.DefaultSocketPath
	var mcpSocketPath string
	var runtimeRoot string
	var serviceInstanceID string
	var gitExecutable string
	var approvedRoot string
	var repositoryID string
	var repositoryPrimary string
	var worktreeRoot string
	var repositoryDefaultBranch string
	var comisSocketPath string
	var comisCredentialFile string
	var comisHandshakeOperationID string
	var codexProfileID string
	var codexExecutable string
	var codexVersion string
	var codexModel string
	var codexEffort string
	var codexTerminalAllowEntry string
	var codexNetwork string
	var codexConcurrency int
	var claudeProfileID string
	var claudeExecutable string
	var claudeVersion string
	var claudeModel string
	var claudeEffort string
	var claudeTerminalAllowEntry string
	var claudeNetwork string
	var claudeConcurrency int
	var claudeConfigDirectory string
	var candidateConfigPath string
	var fixtureDecision string
	preparationTTL := 10 * time.Minute
	var fixtureWorker bool
	var fixtureArtifact string
	var help bool
	var version bool
	flags.StringVar(&databasePath, "database", databasePath, "owner-private SQLite database path")
	flags.StringVar(&socketPath, "socket", socketPath, "owner-only operator Unix socket path")
	flags.StringVar(&mcpSocketPath, "mcp-socket", "", "owner-only MCP facade Unix socket path")
	flags.StringVar(&runtimeRoot, "runtime-root", "", "owner-only per-task attachment root")
	flags.StringVar(&serviceInstanceID, "service-instance", "", "exact Comis service instance identity")
	flags.StringVar(&gitExecutable, "git-executable", "", "absolute Git executable path")
	flags.StringVar(&approvedRoot, "approved-root", "", "approved repository root")
	flags.StringVar(&repositoryID, "repository-id", "", "opaque configured repository identity")
	flags.StringVar(&repositoryPrimary, "repository-primary", "", "canonical primary checkout path")
	flags.StringVar(&worktreeRoot, "worktree-root", "", "canonical dedicated worktree parent")
	flags.StringVar(&repositoryDefaultBranch, "repository-default-branch", "", "configured local default branch")
	flags.StringVar(&comisSocketPath, "comis-socket", "", "owner-only Comis control Unix socket")
	flags.StringVar(&comisCredentialFile, "comis-credential-file", "", "owner-private Comis bearer file")
	flags.StringVar(&comisHandshakeOperationID, "comis-handshake-operation", "", "stable handshake operation identity")
	flags.DurationVar(&preparationTTL, "preparation-ttl", preparationTTL, "activation preparation lifetime")
	flags.StringVar(&codexProfileID, "codex-profile", "", "exact reviewed worker profile identity")
	flags.StringVar(&codexExecutable, "codex-executable", "", "canonical Codex executable path")
	flags.StringVar(&codexVersion, "codex-version", "", "exact reviewed codex-cli version output")
	flags.StringVar(&codexModel, "codex-model", "", "pinned Codex model")
	flags.StringVar(&codexEffort, "codex-effort", "", "pinned reasoning effort")
	flags.StringVar(&codexTerminalAllowEntry, "codex-terminal-allow-entry", "", "reviewed Comis terminal allow-entry identity")
	flags.StringVar(&codexNetwork, "codex-network", "", "reviewed network posture")
	flags.IntVar(&codexConcurrency, "codex-concurrency", 0, "reviewed profile concurrency limit")
	flags.StringVar(&claudeProfileID, "claude-profile", "", "exact reviewed Claude Code profile identity")
	flags.StringVar(&claudeExecutable, "claude-executable", "", "canonical Claude Code executable path")
	flags.StringVar(&claudeVersion, "claude-version", "", "exact reviewed Claude Code version output")
	flags.StringVar(&claudeModel, "claude-model", "", "pinned Claude model")
	flags.StringVar(&claudeEffort, "claude-effort", "", "pinned Claude reasoning effort")
	flags.StringVar(&claudeTerminalAllowEntry, "claude-terminal-allow-entry", "", "reviewed Comis terminal allow-entry identity")
	flags.StringVar(&claudeNetwork, "claude-network", "", "reviewed Claude network posture")
	flags.IntVar(&claudeConcurrency, "claude-concurrency", 0, "reviewed Claude profile concurrency limit")
	flags.StringVar(&claudeConfigDirectory, "claude-config-directory", "", "owner-private Claude Code config directory")
	flags.StringVar(&candidateConfigPath, "candidate-config", "", "owner-private validation and forge policy")
	flags.BoolVar(&fixtureWorker, "fixture-worker", false, "enable deterministic in-process worker")
	flags.StringVar(&fixtureDecision, "fixture-decision", "", "fixed deterministic worker decision response")
	flags.StringVar(&fixtureArtifact, "fixture-artifact", "", "single deterministic candidate artifact filename")
	flags.BoolVar(&help, "help", false, "show help")
	flags.BoolVar(&help, "h", false, "show help")
	flags.BoolVar(&version, "version", false, "show version")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return writeServiceDiagnostic(stderr, "devcrew-service: invalid service arguments\n", 2)
	}
	preparationTTLConfigured := false
	flags.Visit(func(parsed *flag.Flag) {
		if parsed.Name == "preparation-ttl" {
			preparationTTLConfigured = true
		}
	})
	if help {
		return writeServiceDiagnostic(stdout, serviceUsage, 0)
	}
	if version {
		versionName := config.Version
		if versionName == "" {
			versionName = "dev"
		}
		return writeServiceDiagnostic(stdout, fmt.Sprintf("devcrew-service %s\n", versionName), 0)
	}
	if databasePath == "" || socketPath == "" {
		return writeServiceDiagnostic(stderr, "devcrew-service: service paths are not configured\n", 2)
	}
	installedValues := []string{
		mcpSocketPath, runtimeRoot, serviceInstanceID, gitExecutable, approvedRoot, repositoryID, repositoryPrimary,
		worktreeRoot, repositoryDefaultBranch, comisSocketPath, comisCredentialFile, comisHandshakeOperationID,
		codexProfileID, codexExecutable, codexVersion, codexModel, codexEffort, codexTerminalAllowEntry, codexNetwork,
		candidateConfigPath,
	}
	installed := preparationTTLConfigured || codexConcurrency != 0
	for _, value := range installedValues {
		installed = installed || value != ""
	}
	validNetwork := codexNetwork == string(workers.NetworkDisabled) || codexNetwork == string(workers.NetworkRestricted) || codexNetwork == string(workers.NetworkHost)
	if installed && (preparationTTL <= 0 || preparationTTL > 24*time.Hour || codexConcurrency < 1 || codexConcurrency > 64 || !validNetwork) {
		return writeServiceDiagnostic(stderr, "devcrew-service: installed composition is incomplete\nHint: configure every repository, MCP, Comis, and Codex option\n", 2)
	}
	if installed {
		for _, value := range installedValues {
			if value == "" {
				return writeServiceDiagnostic(stderr, "devcrew-service: installed composition is incomplete\nHint: configure every repository, MCP, Comis, and Codex option\n", 2)
			}
		}
	}
	claudeValues := []string{
		claudeProfileID, claudeExecutable, claudeVersion, claudeModel, claudeEffort,
		claudeTerminalAllowEntry, claudeNetwork, claudeConfigDirectory,
	}
	claudeConfigured := claudeConcurrency != 0
	for _, value := range claudeValues {
		claudeConfigured = claudeConfigured || value != ""
	}
	validClaudeNetwork := claudeNetwork == string(workers.NetworkDisabled) || claudeNetwork == string(workers.NetworkRestricted) || claudeNetwork == string(workers.NetworkHost)
	if claudeConfigured && (!installed || claudeConcurrency < 1 || claudeConcurrency > 64 || !validClaudeNetwork) {
		return writeServiceDiagnostic(stderr, "devcrew-service: Claude composition is incomplete\nHint: configure every Claude worker option together with the installed service\n", 2)
	}
	if claudeConfigured {
		for _, value := range claudeValues {
			if value == "" {
				return writeServiceDiagnostic(stderr, "devcrew-service: Claude composition is incomplete\nHint: configure every Claude worker option together with the installed service\n", 2)
			}
		}
	}
	fixtureConfigured := fixtureWorker || fixtureDecision != "" || fixtureArtifact != ""
	if fixtureConfigured && (!installed || !fixtureWorker || strings.TrimSpace(fixtureDecision) == "" || fixtureArtifact == "") {
		return writeServiceDiagnostic(stderr, "devcrew-service: deterministic fixture composition is incomplete\nHint: configure the fixture worker, decision, and artifact together\n", 2)
	}
	runService := config.RunService
	if runService == nil {
		runService = Run
	}
	serviceConfig := Config{DatabasePath: databasePath, SocketPath: socketPath}
	if installed {
		serviceConfig.MCPSocketPath = mcpSocketPath
		serviceConfig.RuntimeRoot = runtimeRoot
		serviceConfig.ServiceInstanceID = serviceInstanceID
		serviceConfig.PreparationTTL = preparationTTL
		serviceConfig.RepositoryComposition = &RepositoryComposition{
			GitExecutable: gitExecutable, ApprovedRoot: approvedRoot, RepositoryID: repositoryID,
			PrimaryCheckout: repositoryPrimary, WorktreeRoot: worktreeRoot, DefaultBranch: repositoryDefaultBranch,
		}
		serviceConfig.ComisComposition = &ComisComposition{
			SocketPath: comisSocketPath, CredentialFile: comisCredentialFile,
			HandshakeOperationID: comisHandshakeOperationID,
		}
		serviceConfig.CodexComposition = &CodexComposition{
			ProfileID: codexProfileID, Executable: codexExecutable, ExpectedVersion: codexVersion,
			Model: codexModel, Effort: codexEffort, TerminalAllowEntryID: codexTerminalAllowEntry,
			Network: workers.NetworkPosture(codexNetwork), ConcurrencyLimit: codexConcurrency,
		}
		if claudeConfigured {
			serviceConfig.ClaudeComposition = &ClaudeComposition{
				ProfileID: claudeProfileID, Executable: claudeExecutable, ExpectedVersion: claudeVersion,
				Model: claudeModel, Effort: claudeEffort, TerminalAllowEntryID: claudeTerminalAllowEntry,
				Network: workers.NetworkPosture(claudeNetwork), ConcurrencyLimit: claudeConcurrency,
				ConfigDirectory: claudeConfigDirectory,
			}
		}
		validationComposition, forgeComposition, readErr := readCandidateComposition(candidateConfigPath)
		if readErr != nil {
			return writeServiceDiagnostic(stderr, "devcrew-service: candidate configuration is invalid\nHint: provide one canonical owner-private reviewed candidate policy\n", 2)
		}
		serviceConfig.ValidationComposition = validationComposition
		serviceConfig.ForgeComposition = forgeComposition
		if fixtureConfigured {
			serviceConfig.FixtureComposition = &FixtureComposition{
				Decision: fixtureDecision, ArtifactRelativePath: fixtureArtifact,
			}
		}
	}
	if err := runService(ctx, serviceConfig); err != nil {
		cause := serviceFailureCause(err)
		causeLine := ""
		if cause != "" {
			causeLine = fmt.Sprintf("Failure cause: %s\n", cause)
		}
		return writeServiceDiagnostic(stderr, fmt.Sprintf(
			"devcrew-service: service stopped with an error\nFailure class: %s\n%sHint: inspect local configuration and service health\n",
			serviceFailureClass(err), causeLine,
		), 1)
	}
	return 0
}

func serviceFailureCause(err error) string {
	message := err.Error()
	causes := []struct {
		match string
		name  string
	}{
		{"validate task candidate: read task", "candidate_task_unavailable"},
		{"validate task candidate: durable worktree is unavailable", "candidate_worktree_unavailable"},
		{"validate task candidate: reviewed profile is unavailable", "candidate_profile_unavailable"},
		{"validate task candidate: decision inventory is unavailable", "candidate_decision_inventory_unavailable"},
		{"validate task candidate: unresolved decisions remain", "candidate_decisions_unresolved"},
		{"validate task candidate: Git evidence is unavailable", "candidate_git_evidence_unavailable"},
		{"validate task candidate: validation process identity is unavailable", "candidate_validation_identity_unavailable"},
		{"validate task candidate: validation receipt is incomplete", "candidate_validation_receipt_incomplete"},
		{"validate task candidate: Git evidence changed during validation", "candidate_git_evidence_changed"},
		{"validate task candidate: pull-request truth is unavailable", "candidate_pull_request_truth_unavailable"},
		{"validate task candidate: report artifact is unavailable", "candidate_report_artifact_unavailable"},
		{"candidate evidence was not accepted", "candidate_evidence_rejected"},
		{"durable task queue is unavailable", "candidate_queue_unavailable"},
	}
	for _, cause := range causes {
		if strings.Contains(message, cause.match) {
			return cause.name
		}
	}
	return ""
}

func serviceFailureClass(err error) string {
	message := err.Error()
	switch {
	case strings.Contains(message, "candidate supervisor: validate task candidate: pull-request truth is unavailable"):
		return "candidate_pull_request_truth"
	case strings.Contains(message, "candidate supervisor: candidate evidence was not accepted"):
		return "candidate_evidence_rejected"
	case strings.Contains(message, "candidate supervisor"):
		return "candidate_supervision"
	case strings.Contains(message, "run service validation recovery"):
		return "validation_process_recovery"
	case strings.Contains(message, "run service startup reconciliation"):
		return "startup_reconciliation"
	case strings.Contains(message, "run service local endpoint"):
		return "operator_endpoint"
	case strings.Contains(message, "run service MCP endpoint"):
		return "mcp_endpoint"
	case strings.Contains(message, "run service store"):
		return "state_store"
	default:
		return "service_runtime"
	}
}

func writeServiceDiagnostic(destination io.Writer, message string, successCode int) int {
	if destination == nil {
		return 1
	}
	if _, err := io.WriteString(destination, message); err != nil {
		return 1
	}
	return successCode
}
