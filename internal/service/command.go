package service

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	preparationTTL := 10 * time.Minute
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
	}
	if err := runService(ctx, serviceConfig); err != nil {
		return writeServiceDiagnostic(stderr, "devcrew-service: service stopped with an error\nHint: inspect local configuration and service health\n", 1)
	}
	return 0
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
