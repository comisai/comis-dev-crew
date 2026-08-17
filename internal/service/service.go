// Package service composes the sole durable daemon authority.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/delivery"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/store/sqlite"
	"github.com/comisai/comis-dev-crew/internal/validation"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

const (
	comisReportPollInterval   = 250 * time.Millisecond
	comisReportMinimumBackoff = 100 * time.Millisecond
	comisReportMaximumBackoff = 5 * time.Second
	comisRequestTimeout       = 5 * time.Second
	// Well inside the host's own staleness bound, so one missed sweep — a slow
	// store read, a reconnect — never makes a healthy service look departed.
	comisLivenessInterval = 60 * time.Second
	comisMinimumBackoff   = 100 * time.Millisecond
	comisMaximumBackoff   = time.Second
	fixturePollInterval   = 25 * time.Millisecond
)

// ComisControl is the single persistent authenticated connection supervised
// by the service. The concrete control adapter also carries durable reports.
type ComisControl interface {
	comiswire.ReportSender
	comiswire.EvidenceSender
	comiswire.HeartbeatSender
	comiswire.AttentionResponseReceiver
	application.ManagedRunReleaser
	application.HostIntegrationStatus
	Run(context.Context) error
}

// Config identifies the service-owned database and operator endpoint.
type Config struct {
	DatabasePath             string
	SocketPath               string
	MCPSocketPath            string
	RuntimeRoot              string
	ServiceInstanceID        string
	Repositories             application.RepositoryCatalog
	WorkerProfiles           application.WorkerProfileValidator
	ValidationProfiles       application.ValidationProfileValidator
	Workspaces               application.WorkspacePreparer
	RuntimeAttachments       application.RuntimeAttachmentCoordinator
	WorkerHarnesses          application.WorkerHarnessResolver
	TaskIDs                  application.TaskIDSource
	RegistrationNonces       application.RegistrationNonceSource
	PreparationTTL           time.Duration
	Clock                    application.Clock
	ComisControl             ComisControl
	RepositoryComposition    *RepositoryComposition
	ComisComposition         *ComisComposition
	CodexComposition         *CodexComposition
	ClaudeComposition        *ClaudeComposition
	ValidationComposition    *ValidationComposition
	ForgeComposition         *ForgeComposition
	FixtureComposition       *FixtureComposition
	Ready                    func()
	candidateGit             candidateGitInspector
	workspaceInspector       application.WorkspaceInspector
	reconciliationInspector  application.ReconciliationWorkspaceManager
	validationCatalog        *validation.Catalog
	validationMaxOutputBytes int64
	validationPollInterval   time.Duration
	pullRequests             candidatePullRequestDeliverer
	cleanupRemover           application.DeliveredWorkspaceRemover
	cleanupForge             application.PullRequestDeliveryVerifier
	fixtureCandidatePreparer fixtureCandidatePreparer
}

// RepositoryComposition is the installed single-repository fixture lane.
type RepositoryComposition struct {
	GitExecutable   string
	ApprovedRoot    string
	RepositoryID    string
	PrimaryCheckout string
	WorktreeRoot    string
	DefaultBranch   string
}

// ComisComposition identifies the installed authenticated control lane without
// placing its protected bearer on the process command line.
type ComisComposition struct {
	SocketPath           string
	CredentialFile       string
	HandshakeOperationID string
}

// CodexComposition is one exact operator-reviewed production worker profile.
// Lifecycle settling is intentionally not configurable until a trustworthy
// Codex settle signal is ratified.
type CodexComposition struct {
	ProfileID            string
	Executable           string
	ExpectedVersion      string
	Model                string
	Effort               string
	TerminalAllowEntryID string
	Network              workers.NetworkPosture
	ConcurrencyLimit     int
}

// ClaudeComposition is one exact operator-reviewed production worker profile.
// Its owner-private config directory is exposed read-only by the terminal jail.
type ClaudeComposition struct {
	ProfileID            string
	Executable           string
	ExpectedVersion      string
	Model                string
	Effort               string
	TerminalAllowEntryID string
	Network              workers.NetworkPosture
	ConcurrencyLimit     int
	ConfigDirectory      string
}

// ValidationComposition is the immutable operator-reviewed candidate policy.
type ValidationComposition struct {
	Programs       []validation.Program
	Profiles       []validation.Profile
	MaxOutputBytes int64
	PollInterval   time.Duration
}

// ForgeComposition fixes the sole E0 pull-request route and keeps its read and
// push credentials in distinct owner-private files.
type ForgeComposition struct {
	APIBaseURL             string
	Owner                  string
	Repository             string
	RemoteURL              string
	ReadCredentialFile     string
	PushCredentialFile     string
	CredentialDirectory    string
	LocalFixtureRemoteRoot string
	SSHTransportExecutable string
	SSHExecutable          string
	SSHKnownHostsFile      string
}

// FixtureComposition enables the reviewed deterministic worker with one fixed
// local decision response.
type FixtureComposition struct {
	Decision             string
	ArtifactRelativePath string
}

// Run opens the sole writable store and serves canonical operator queries until
// cancellation. It joins every acquired resource before returning.
func Run(ctx context.Context, config Config) (resultErr error) {
	if ctx == nil {
		return errors.New("run service: context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if config.DatabasePath == "" || config.SocketPath == "" {
		return errors.New("run service: database and socket paths are required")
	}
	configured, err := composeInstalledRuntime(ctx, config)
	if err != nil {
		return err
	}
	config = configured
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	store, err := sqlite.Open(ctx, config.DatabasePath)
	if err != nil {
		return fmt.Errorf("run service store: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, store.Close())
	}()
	var attachmentSupervisor *runtimeAttachmentCoordinator
	if config.RuntimeAttachments == nil && config.RuntimeRoot != "" {
		attachmentSupervisor, err = newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
			RuntimeRoot: config.RuntimeRoot, Store: store, Clock: clock,
			NewCredential:           func() (string, error) { return randomIdentity("runtime-credential", 16) },
			NewAttentionOperationID: func() (string, error) { return randomIdentity("attention-response", 16) },
		})
		if err != nil {
			return fmt.Errorf("run service runtime attachments: %w", err)
		}
		config.RuntimeAttachments = attachmentSupervisor
		if err := attachmentSupervisor.recoverRuntimeRelayIdentityUpgrades(ctx); err != nil {
			return fmt.Errorf("run service runtime relay identity upgrade: %w", err)
		}
	} else {
		upgrades, upgradeErr := store.ListRuntimeRelayIdentityUpgrades(ctx)
		if upgradeErr != nil || len(upgrades) != 0 {
			return errors.New("run service runtime relay identity upgrade requires service-owned attachments")
		}
	}
	reconciler, err := application.NewStartupReconciler(application.StartupReconcilerConfig{Store: store, Clock: clock})
	if err != nil {
		return fmt.Errorf("run service startup reconciler: %w", err)
	}
	if _, err := reconciler.Reconcile(ctx); err != nil {
		return fmt.Errorf("run service startup reconciliation: %w", err)
	}
	var candidate *candidateSupervisor
	if config.validationCatalog != nil {
		runner, runnerErr := validation.NewRunner(validation.RunnerConfig{
			Catalog: config.validationCatalog, Processes: store,
			MaxOutputBytes: config.validationMaxOutputBytes, Clock: clock,
		})
		if runnerErr != nil {
			return fmt.Errorf("run service validation runner: %w", runnerErr)
		}
		if _, recoverErr := runner.Recover(ctx); recoverErr != nil {
			return fmt.Errorf("run service validation recovery: %w", recoverErr)
		}
		candidate, runnerErr = newCandidateSupervisor(candidateSupervisorConfig{
			Store: store, Git: config.candidateGit, Catalog: config.validationCatalog,
			Runner: runner, PullRequests: config.pullRequests, InspectArtifact: delivery.InspectReportArtifact,
			NewValidationOperationID: func() (string, error) { return randomIdentity("validation", 16) },
			Clock:                    clock, PollInterval: config.validationPollInterval,
		})
		if runnerErr != nil {
			return fmt.Errorf("run service candidate supervisor: %w", runnerErr)
		}
	}
	mutations, err := composeMutations(config, store, clock)
	if err != nil {
		return err
	}
	if attachmentSupervisor != nil {
		if err := attachmentSupervisor.SetRecoveryAcknowledger(mutations); err != nil {
			return fmt.Errorf("run service runtime attachment recovery: %w", err)
		}
	}
	var interventions *application.Interventions
	if config.workspaceInspector != nil {
		interventions, err = application.NewInterventions(application.InterventionConfig{
			Store: store, Workspaces: config.workspaceInspector, Clock: clock,
		})
		if err != nil {
			return fmt.Errorf("run service intervention coordinator: %w", err)
		}
	}
	var reconciliation *application.TaskCandidateReconciler
	if config.reconciliationInspector != nil {
		reconciliation, err = application.NewTaskCandidateReconciler(application.TaskCandidateReconcilerConfig{
			Store: store, Workspaces: config.reconciliationInspector, Clock: clock,
		})
		if err != nil {
			return fmt.Errorf("run service task reconciliation coordinator: %w", err)
		}
	}
	var controlMutations comiswire.DurableControlMutations
	if mutations != nil {
		controlMutations = mutations
	}
	if config.RepositoryComposition != nil {
		launchSupervisor, supervisorErr := newProductionLaunchSupervisor(productionLaunchSupervisorConfig{
			Store: store, Mutations: mutations, Harnesses: config.WorkerHarnesses,
		})
		if supervisorErr != nil {
			return fmt.Errorf("run service production launch supervisor: %w", supervisorErr)
		}
		controlMutations = launchSupervisor
	}
	control, err := composeComisControl(config, controlMutations)
	if err != nil {
		return err
	}
	if attachmentSupervisor != nil && control != nil {
		if err := attachmentSupervisor.SetAttentionResponseReceiver(control); err != nil {
			return fmt.Errorf("run service runtime attention responses: %w", err)
		}
	}
	queries, err := application.NewQueries(application.QueryConfig{
		Repository: store, Harnesses: config.WorkerHarnesses, Host: control,
		ReconciliationWorkspaces: config.reconciliationInspector, Clock: clock,
	})
	if err != nil {
		return fmt.Errorf("run service queries: %w", err)
	}
	var cleanup *application.CleanupCoordinator
	if config.cleanupRemover != nil || config.cleanupForge != nil {
		if control == nil || config.workspaceInspector == nil || config.cleanupRemover == nil || config.cleanupForge == nil {
			return errors.New("run service: cleanup composition is incomplete")
		}
		cleanup, err = application.NewCleanupCoordinator(application.CleanupCoordinatorConfig{
			Store: store, Workspaces: config.workspaceInspector, Forge: config.cleanupForge,
			Releaser: control, Attachments: config.RuntimeAttachments,
			Remover: config.cleanupRemover, Clock: clock,
		})
		if err != nil {
			return fmt.Errorf("run service cleanup coordinator: %w", err)
		}
	}
	var fixture *fixtureSupervisor
	if config.FixtureComposition != nil {
		fixture, err = newFixtureSupervisor(fixtureSupervisorConfig{
			Store: store, Mutations: mutations, Clock: clock,
			Decision: config.FixtureComposition.Decision, PollInterval: fixturePollInterval,
			CandidatePreparer:    config.fixtureCandidatePreparer,
			ArtifactRelativePath: config.FixtureComposition.ArtifactRelativePath,
			NewCredential:        func() (string, error) { return randomIdentity("fixture-reporter", 16) },
		})
		if err != nil {
			return fmt.Errorf("run service fixture composition: %w", err)
		}
	}
	handlerConfig := localapi.HandlerConfig{Queries: queries, Clock: clock}
	if mutations != nil {
		handlerConfig.Mutations = mutations
		handlerConfig.ServiceInstanceID = config.ServiceInstanceID
	}
	if interventions != nil {
		handlerConfig.Interventions = interventions
	}
	if reconciliation != nil {
		handlerConfig.Reconciliation = reconciliation
	}
	if cleanup != nil {
		handlerConfig.Cleanup = cleanup
	}
	handler, err := localapi.NewHandler(handlerConfig)
	if err != nil {
		return fmt.Errorf("run service local handler: %w", err)
	}
	operatorServer, err := localapi.Listen(config.SocketPath, localapi.CallerOperatorCLI, handler)
	if err != nil {
		return fmt.Errorf("run service local endpoint: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, operatorServer.Close())
	}()
	servers := []*localapi.Server{operatorServer}
	if config.MCPSocketPath != "" {
		mcpServer, listenErr := localapi.Listen(config.MCPSocketPath, localapi.CallerMCPFacade, handler)
		if listenErr != nil {
			return fmt.Errorf("run service MCP endpoint: %w", listenErr)
		}
		defer func() { resultErr = errors.Join(resultErr, mcpServer.Close()) }()
		servers = append(servers, mcpServer)
	}
	if control == nil {
		if attachmentSupervisor != nil {
			return serveServiceComponents(
				ctx, servers, []func(context.Context) error{attachmentSupervisor.Run},
				attachmentSupervisor.waitForRecovery, config.Ready,
			)
		}
		if config.Ready != nil {
			config.Ready()
		}
		return serveLocalEndpoints(ctx, servers)
	}
	// One failure path for the three control-plane components. Each constructor
	// only refuses a dependency this composition supplies as a constant, so a
	// separate branch per component is three unreachable returns saying the same
	// thing: the control plane could not be built.
	forwarder, forwarderErr := comiswire.NewReportForwarder(comiswire.ReportForwarderConfig{
		Outbox: store, Sender: control, Clock: clock,
		PollInterval: comisReportPollInterval, MinimumBackoff: comisReportMinimumBackoff,
		MaximumBackoff: comisReportMaximumBackoff,
	})
	evidenceForwarder, evidenceErr := comiswire.NewEvidenceForwarder(comiswire.EvidenceForwarderConfig{
		Outbox: store, Sender: control, Clock: clock,
		PollInterval: comisReportPollInterval, MinimumBackoff: comisReportMinimumBackoff,
		MaximumBackoff: comisReportMaximumBackoff,
	})
	liveness, livenessErr := comiswire.NewLivenessReporter(comiswire.LivenessReporterConfig{
		Tasks: store, Sender: control, Clock: clock, Interval: comisLivenessInterval,
	})
	if err := errors.Join(forwarderErr, evidenceErr, livenessErr); err != nil {
		return fmt.Errorf("run service Comis control components: %w", err)
	}
	components := []func(context.Context) error{
		control.Run,
		evidenceForwarder.Run,
		forwarder.Run,
		liveness.Run,
	}
	if attachmentSupervisor != nil {
		components = append(components, attachmentSupervisor.Run)
	}
	if fixture != nil {
		components = append(components, fixture.Run)
	}
	if candidate != nil {
		components = append(components, candidate.Run)
	}
	var beforeReady func(context.Context) error
	if attachmentSupervisor != nil {
		beforeReady = attachmentSupervisor.waitForRecovery
	}
	return serveServiceComponents(ctx, servers, components, beforeReady, config.Ready)
}

func composeMutations(config Config, store *sqlite.Store, clock application.Clock) (*application.Mutations, error) {
	configured := config.Repositories != nil || config.Workspaces != nil || config.TaskIDs != nil ||
		config.RuntimeAttachments != nil || config.RegistrationNonces != nil || config.PreparationTTL != 0 || config.ServiceInstanceID != ""
	if !configured {
		if config.MCPSocketPath != "" {
			return nil, errors.New("run service: MCP endpoint requires mutation configuration")
		}
		return nil, nil
	}
	mutations, err := application.NewMutations(application.MutationConfig{
		Store: store, Repositories: config.Repositories,
		WorkerProfiles: config.WorkerProfiles, ValidationProfiles: config.ValidationProfiles,
		Workspaces: config.Workspaces, TaskIDs: config.TaskIDs,
		RuntimeAttachments: config.RuntimeAttachments,
		RegistrationNonces: config.RegistrationNonces,
		PreparationTTL:     config.PreparationTTL,
		Clock:              clock,
	})
	if err != nil {
		return nil, fmt.Errorf("run service mutation coordinator: %w", err)
	}
	if config.ServiceInstanceID == "" {
		return nil, errors.New("run service: mutation service instance is required")
	}
	return mutations, nil
}

func serveLocalEndpoints(ctx context.Context, servers []*localapi.Server) error {
	if len(servers) == 1 {
		return servers[0].Serve(ctx)
	}
	serveContext, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(servers))
	for _, server := range servers {
		go func(endpoint *localapi.Server) { results <- endpoint.Serve(serveContext) }(server)
	}
	var resultErr error
	for range servers {
		err := <-results
		resultErr = errors.Join(resultErr, err)
		cancel()
		for _, server := range servers {
			resultErr = errors.Join(resultErr, server.Close())
		}
	}
	return resultErr
}
