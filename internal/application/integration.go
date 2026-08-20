package application

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// IntegrationStrategy is the closed set of operator-reviewed Git operations.
// A caller selects an initiative, never an argv fragment or strategy.
type IntegrationStrategy string

const (
	IntegrationMerge      IntegrationStrategy = "merge"
	IntegrationRebase     IntegrationStrategy = "rebase"
	IntegrationCherryPick IntegrationStrategy = "cherry_pick"
)

// IntegrationOutcome is the closed durable result of applying one candidate.
type IntegrationOutcome string

const (
	IntegrationApplied    IntegrationOutcome = "applied"
	IntegrationConflicted IntegrationOutcome = "conflicted"
)

// IntegrationPolicyResolver maps immutable operator policy identity onto one
// reviewed strategy. It never receives task or model-authored content.
type IntegrationPolicyResolver func(string) (IntegrationStrategy, error)

// ApplyIntegrationCandidateCommand binds one operation to exact candidate and
// target heads. Paths and strategy are intentionally absent.
type ApplyIntegrationCandidateCommand struct {
	OperationID             string
	InitiativeHandle        string
	IntegrationTaskHandle   string
	CandidateTaskHandle     string
	CandidateHead           string
	ExpectedIntegrationHead string
}

// IntegrationTargetReference is the store-resolved dedicated writer target.
type IntegrationTargetReference struct {
	TaskHandle   string
	RepositoryID string
	WorktreePath string
	ExpectedHead string
}

// IntegrationCandidateReference is one immutable, evidence-backed task head.
type IntegrationCandidateReference struct {
	TaskHandle     string
	RepositoryID   string
	WorktreePath   string
	BaseRevision   string
	HeadRevision   string
	EvidenceDigest string
}

// IntegrationAdapterRequest is the complete typed Git mutation contract.
type IntegrationAdapterRequest struct {
	OperationID string
	Strategy    IntegrationStrategy
	Target      IntegrationTargetReference
	Candidate   IntegrationCandidateReference
}

// IntegrationAdapterResult reports either one exact new head or bounded
// actionable conflicts. It cannot report both.
type IntegrationAdapterResult struct {
	Outcome       IntegrationOutcome
	PreviousHead  string
	ResultingHead string
	ConflictPaths []string
}

// IntegrationAdapter owns the fixed Git command vocabulary.
type IntegrationAdapter interface {
	ApplyIntegrationCandidate(context.Context, IntegrationAdapterRequest) (IntegrationAdapterResult, error)
}

// IntegrationReservationRequest asks the sole writer to resolve authority and
// durably reserve an exact operation before Git is changed.
type IntegrationReservationRequest struct {
	Command       ApplyIntegrationCandidateCommand
	PolicyID      string
	Strategy      IntegrationStrategy
	SubjectDigest string
	At            time.Time
}

// ReservedIntegrationApplication contains only store-verified identities. A
// result is present only when the exact operation already completed.
type ReservedIntegrationApplication struct {
	OperationID           string
	SubjectDigest         string
	InitiativeHandle      string
	IntegrationTaskHandle string
	PolicyID              string
	Strategy              IntegrationStrategy
	Target                IntegrationTargetReference
	Candidate             IntegrationCandidateReference
	EvidenceExpiresAt     time.Time
	ReservedAt            time.Time
	Result                *IntegrationApplicationResult
}

// AdapterRequest projects a reservation onto the mutation boundary.
func (reserved ReservedIntegrationApplication) AdapterRequest() IntegrationAdapterRequest {
	return IntegrationAdapterRequest{
		OperationID: reserved.OperationID, Strategy: reserved.Strategy,
		Target: reserved.Target, Candidate: reserved.Candidate,
	}
}

// IntegrationCompletion joins the reserved identity to one adapter result.
type IntegrationCompletion struct {
	Reservation   ReservedIntegrationApplication
	AdapterResult IntegrationAdapterResult
	At            time.Time
}

// IntegrationApplicationResult is the durable, replayable candidate outcome.
type IntegrationApplicationResult struct {
	OperationID           string                        `json:"operationId"`
	InitiativeHandle      string                        `json:"initiativeHandle"`
	IntegrationTaskHandle string                        `json:"integrationTaskHandle"`
	Candidate             IntegrationCandidateReference `json:"candidate"`
	Strategy              IntegrationStrategy           `json:"strategy"`
	Outcome               IntegrationOutcome            `json:"outcome"`
	PreviousHead          string                        `json:"previousHead"`
	ResultingHead         string                        `json:"resultingHead,omitempty"`
	ConflictPaths         []string                      `json:"conflictPaths,omitempty"`
	StateVersion          int64                         `json:"stateVersion"`
	CompletedAt           time.Time                     `json:"completedAt"`
}

// IntegrationStore owns policy lookup, pre-mutation reservation, and exact
// result persistence under the service's single-writer transaction boundary.
type IntegrationStore interface {
	IntegrationPolicy(context.Context, string) (string, error)
	ReserveIntegrationApplication(context.Context, IntegrationReservationRequest) (ReservedIntegrationApplication, error)
	CompleteIntegrationApplication(context.Context, IntegrationCompletion) (IntegrationApplicationResult, error)
}

// IntegrationConfig supplies the three authorities needed by the coordinator.
type IntegrationConfig struct {
	Store    IntegrationStore
	Adapter  IntegrationAdapter
	Policies IntegrationPolicyResolver
	Clock    Clock
}

// Integrations coordinates one reserved single-writer candidate application.
type Integrations struct {
	store    IntegrationStore
	adapter  IntegrationAdapter
	policies IntegrationPolicyResolver
	clock    Clock
}

// NewIntegrations validates the integration composition.
func NewIntegrations(config IntegrationConfig) (*Integrations, error) {
	if config.Store == nil || config.Adapter == nil || config.Policies == nil || config.Clock == nil {
		return nil, errors.New("create integrations: store, adapter, policies, and clock are required")
	}
	return &Integrations{store: config.Store, adapter: config.Adapter, policies: config.Policies, clock: config.Clock}, nil
}

// ApplyCandidate reserves exact authority, applies one typed candidate, and
// persists the result. Exact replay never crosses the Git mutation boundary.
func (integrations *Integrations) ApplyCandidate(
	ctx context.Context,
	command ApplyIntegrationCandidateCommand,
) (IntegrationApplicationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return IntegrationApplicationResult{}, err
	}
	if err := validateIntegrationCommand(command); err != nil {
		return IntegrationApplicationResult{}, mutationValidationFailure(err.Error())
	}
	policyID, err := integrations.store.IntegrationPolicy(ctx, command.InitiativeHandle)
	if err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration policy is unavailable", cause: err}
	}
	strategy, err := integrations.policies(policyID)
	if err != nil || !strategy.valid() {
		return IntegrationApplicationResult{}, mutationValidationFailure("integration policy is not reviewed")
	}
	subjectDigest, err := digestMutationSubject(struct {
		Command  ApplyIntegrationCandidateCommand
		PolicyID string
		Strategy IntegrationStrategy
	}{Command: command, PolicyID: policyID, Strategy: strategy})
	if err != nil {
		return IntegrationApplicationResult{}, mutationValidationFailure("integration subject cannot be encoded")
	}
	at := integrations.clock().UTC()
	if at.IsZero() {
		return IntegrationApplicationResult{}, errors.New("apply integration candidate: clock is invalid")
	}
	reserved, err := integrations.store.ReserveIntegrationApplication(ctx, IntegrationReservationRequest{
		Command: command, PolicyID: policyID, Strategy: strategy, SubjectDigest: subjectDigest, At: at,
	})
	if err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration reservation failed", cause: err}
	}
	if err := validateIntegrationReservation(reserved, command, policyID, strategy, subjectDigest); err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration reservation differs", cause: err}
	}
	if reserved.Result != nil {
		if err := validateIntegrationResult(*reserved.Result, reserved); err != nil {
			return IntegrationApplicationResult{}, &dependencyFailure{message: "integration replay differs", cause: err}
		}
		return cloneIntegrationResult(*reserved.Result), nil
	}
	if !at.Before(reserved.EvidenceExpiresAt) {
		return IntegrationApplicationResult{}, mutationValidationFailure("integration candidate evidence expired")
	}
	adapterResult, err := integrations.adapter.ApplyIntegrationCandidate(ctx, reserved.AdapterRequest())
	if err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration adapter failed", cause: err}
	}
	if err := validateIntegrationAdapterResult(adapterResult, reserved); err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration adapter result differs", cause: err}
	}
	completed, err := integrations.store.CompleteIntegrationApplication(ctx, IntegrationCompletion{
		Reservation: reserved, AdapterResult: cloneIntegrationAdapterResult(adapterResult), At: at,
	})
	if err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration completion failed", cause: err}
	}
	if err := validateIntegrationResult(completed, reserved); err != nil {
		return IntegrationApplicationResult{}, &dependencyFailure{message: "integration completion differs", cause: err}
	}
	return cloneIntegrationResult(completed), nil
}

func (strategy IntegrationStrategy) valid() bool {
	return strategy == IntegrationMerge || strategy == IntegrationRebase || strategy == IntegrationCherryPick
}

func validateIntegrationCommand(command ApplyIntegrationCandidateCommand) error {
	if domain.ValidateOperationID(command.OperationID) != nil || domain.ValidateTaskHandle(command.InitiativeHandle) != nil ||
		domain.ValidateTaskHandle(command.IntegrationTaskHandle) != nil || domain.ValidateTaskHandle(command.CandidateTaskHandle) != nil ||
		command.IntegrationTaskHandle == command.CandidateTaskHandle || domain.ValidateGitRevision(command.CandidateHead) != nil ||
		domain.ValidateGitRevision(command.ExpectedIntegrationHead) != nil {
		return errors.New("integration command identity is invalid")
	}
	return nil
}

func validateIntegrationReservation(
	reserved ReservedIntegrationApplication,
	command ApplyIntegrationCandidateCommand,
	policyID string,
	strategy IntegrationStrategy,
	subjectDigest string,
) error {
	if reserved.OperationID != command.OperationID || reserved.SubjectDigest != subjectDigest ||
		reserved.InitiativeHandle != command.InitiativeHandle || reserved.IntegrationTaskHandle != command.IntegrationTaskHandle ||
		reserved.PolicyID != policyID || reserved.Strategy != strategy || reserved.Candidate.TaskHandle != command.CandidateTaskHandle ||
		reserved.Candidate.HeadRevision != command.CandidateHead || reserved.Target.ExpectedHead != command.ExpectedIntegrationHead ||
		reserved.Target.TaskHandle != command.IntegrationTaskHandle || reserved.Target.RepositoryID == "" ||
		reserved.Target.RepositoryID != reserved.Candidate.RepositoryID ||
		reserved.Target.WorktreePath == reserved.Candidate.WorktreePath || !canonicalAbsolutePath(reserved.Target.WorktreePath) ||
		!canonicalAbsolutePath(reserved.Candidate.WorktreePath) || domain.ValidateGitRevision(reserved.Candidate.BaseRevision) != nil {
		return errors.New("reserved integration identity is invalid")
	}
	if domain.ValidateBriefRevisionHash(reserved.Candidate.EvidenceDigest) != nil || reserved.EvidenceExpiresAt.IsZero() ||
		reserved.EvidenceExpiresAt.Location() != time.UTC || reserved.ReservedAt.IsZero() || reserved.ReservedAt.Location() != time.UTC ||
		!reserved.ReservedAt.Before(reserved.EvidenceExpiresAt) {
		return errors.New("reserved integration evidence is invalid")
	}
	return nil
}

func validateIntegrationAdapterResult(result IntegrationAdapterResult, reserved ReservedIntegrationApplication) error {
	if result.PreviousHead != reserved.Target.ExpectedHead {
		return errors.New("integration previous head differs")
	}
	switch result.Outcome {
	case IntegrationApplied:
		if domain.ValidateGitRevision(result.ResultingHead) != nil || result.ResultingHead == result.PreviousHead || len(result.ConflictPaths) != 0 {
			return errors.New("applied integration result is invalid")
		}
	case IntegrationConflicted:
		if result.ResultingHead != "" || !validConflictPaths(result.ConflictPaths) {
			return errors.New("conflicted integration result is invalid")
		}
	default:
		return errors.New("integration outcome is invalid")
	}
	return nil
}

func validateIntegrationResult(result IntegrationApplicationResult, reserved ReservedIntegrationApplication) error {
	if result.OperationID != reserved.OperationID || result.InitiativeHandle != reserved.InitiativeHandle ||
		result.IntegrationTaskHandle != reserved.IntegrationTaskHandle || result.Candidate != reserved.Candidate ||
		result.Strategy != reserved.Strategy || result.PreviousHead != reserved.Target.ExpectedHead || result.StateVersion < 1 ||
		result.CompletedAt.IsZero() || result.CompletedAt.Location() != time.UTC {
		return errors.New("durable integration result identity is invalid")
	}
	return validateIntegrationAdapterResult(IntegrationAdapterResult{
		Outcome: result.Outcome, PreviousHead: result.PreviousHead,
		ResultingHead: result.ResultingHead, ConflictPaths: result.ConflictPaths,
	}, reserved)
}

func validConflictPaths(paths []string) bool {
	if len(paths) == 0 || len(paths) > 256 || !sort.StringsAreSorted(paths) {
		return false
	}
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		clean := filepath.Clean(path)
		if path == "" || len([]byte(path)) > 1024 || filepath.IsAbs(path) || clean != path || clean == "." || clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return false
		}
		if _, exists := seen[path]; exists {
			return false
		}
		seen[path] = struct{}{}
	}
	return true
}

func canonicalAbsolutePath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

func cloneIntegrationAdapterResult(result IntegrationAdapterResult) IntegrationAdapterResult {
	result.ConflictPaths = append([]string(nil), result.ConflictPaths...)
	return result
}

func cloneIntegrationResult(result IntegrationApplicationResult) IntegrationApplicationResult {
	result.ConflictPaths = append([]string(nil), result.ConflictPaths...)
	return result
}
