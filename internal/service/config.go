package service

import (
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/validation"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

// Config identifies the service-owned database and operator endpoint.
type Config struct {
	DatabasePath                    string
	SocketPath                      string
	MCPSocketPath                   string
	RuntimeRoot                     string
	ServiceInstanceID               string
	Repositories                    application.RepositoryCatalog
	WorkerProfiles                  application.WorkerProfileValidator
	WorkerProfileCatalog            application.WorkerProfileCatalog
	ValidationProfiles              application.ValidationProfileValidator
	Workspaces                      application.WorkspacePreparer
	RuntimeAttachments              application.RuntimeAttachmentCoordinator
	WorkerHarnesses                 application.WorkerHarnessResolver
	TaskIDs                         application.TaskIDSource
	RegistrationNonces              application.RegistrationNonceSource
	PreparationTTL                  time.Duration
	MaxConcurrentTasks              int
	MaxConcurrentTasksPerRepository int
	Clock                           application.Clock
	DecisionSurfacing               application.DecisionSurfacingPolicy
	ComisControl                    ComisControl
	RepositoryComposition           *RepositoryComposition
	ComisComposition                *ComisComposition
	CodexComposition                *CodexComposition
	ClaudeComposition               *ClaudeComposition
	ValidationComposition           *ValidationComposition
	ForgeComposition                *ForgeComposition
	FixtureComposition              *FixtureComposition
	Ready                           func()
	// Logger is optional. Without one the service serves exactly as before and
	// records no boundary crossings.
	Logger                   application.BoundaryLogger
	candidateGit             candidateGitInspector
	workspaceInspector       application.WorkspaceInspector
	taskDiffs                application.TaskDiffInspector
	primarySynchronizer      application.PrimarySynchronizer
	reconciliationInspector  application.ReconciliationWorkspaceManager
	validationCatalog        *validation.Catalog
	validationMaxOutputBytes int64
	validationPollInterval   time.Duration
	pullRequests             candidatePullRequestDeliverer
	cleanupRemover           application.DeliveredWorkspaceRemover
	cleanupForge             application.PullRequestDeliveryVerifier
	cleanupLanded            application.LandedEvidenceGatherer
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
