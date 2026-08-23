package application

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// InitiativeHostStateCounts is the complete content-free host vocabulary for
// the member states in one managed-run group. Zero values are explicit zeros.
type InitiativeHostStateCounts struct {
	Preparing         int `json:"preparing"`
	Active            int `json:"active"`
	Waiting           int `json:"waiting"`
	Paused            int `json:"paused"`
	CandidateComplete int `json:"candidateComplete"`
	Succeeded         int `json:"succeeded"`
	Failed            int `json:"failed"`
	Cancelled         int `json:"cancelled"`
	Unknown           int `json:"unknown"`
}

// InitiativeHostRollup is the bounded host projection used to reconcile one
// local initiative. It deliberately carries no task text, paths, or evidence.
type InitiativeHostRollup struct {
	ManagedRunGroupID   string
	MemberManagedRunIDs []string
	StateCounts         InitiativeHostStateCounts
	AttentionCount      int
	ActiveCustodyCount  int
	UpdatedAtMs         int64
}

// InitiativeHostRollupRequest identifies one fresh read on the authenticated
// host connection.
type InitiativeHostRollupRequest struct {
	OperationID       string
	ManagedRunGroupID string
}

// InitiativeHostRollupSource reads host-owned group facts without acquiring
// any mutation authority.
type InitiativeHostRollupSource interface {
	ReadInitiativeHostRollup(context.Context, InitiativeHostRollupRequest) (InitiativeHostRollup, error)
}

// InitiativeHostRecoveryMutation carries the exact host evidence a durable
// store must recheck against its current member rows in one transaction.
type InitiativeHostRecoveryMutation struct {
	InitiativeHandle     string
	ServiceInstanceID    string
	ManagedRunGroupID    string
	MemberManagedRunIDs  []string
	StateCounts          InitiativeHostStateCounts
	ExpectedStateVersion int64
	At                   time.Time
}

// Validate rejects an incomplete or internally inconsistent recovery claim.
func (mutation InitiativeHostRecoveryMutation) Validate() error {
	if domain.ValidateAuthorityReference("initiativeHandle", mutation.InitiativeHandle) != nil ||
		domain.ValidateAuthorityReference("serviceInstanceId", mutation.ServiceInstanceID) != nil ||
		domain.ValidateAuthorityReference("managedRunGroupId", mutation.ManagedRunGroupID) != nil ||
		mutation.ExpectedStateVersion < 1 || mutation.At.IsZero() || mutation.At.Location() != time.UTC ||
		!validManagedRunIdentities(mutation.MemberManagedRunIDs) ||
		!mutation.StateCounts.validForMembers(len(mutation.MemberManagedRunIDs)) {
		return ErrInvalidInput
	}
	return nil
}

// InitiativeHostRecoveryStore owns the read snapshots and the final exact
// compare-and-set that restores an initiative out of unknown.
type InitiativeHostRecoveryStore interface {
	ListInitiatives(context.Context) ([]domain.DevelopmentInitiative, error)
	InitiativeObservation(context.Context, string) (domain.DevelopmentInitiative, []domain.Task, int64, error)
	InitiativeHasPendingComisEgress(context.Context, string) (bool, error)
	CommitInitiativeHostRecovery(context.Context, InitiativeHostRecoveryMutation) (domain.DevelopmentInitiative, error)
}

// InitiativeHostReconciliation reports only counts; group and task identities
// stay in the durable store and content-free boundary records.
type InitiativeHostReconciliation struct {
	Attempted        int
	Recovered        int
	PreservedUnknown int
}

// InitiativeHostReconcilerConfig supplies the two authorities that must agree
// before startup can restore local initiative scheduling.
type InitiativeHostReconcilerConfig struct {
	Store             InitiativeHostRecoveryStore
	Host              InitiativeHostRollupSource
	ServiceInstanceID string
	NewOperationID    func() (string, error)
	Clock             Clock
	AttemptTimeout    time.Duration
	RetryInterval     time.Duration
	Logger            BoundaryLogger
}

// InitiativeHostReconciler compares host facts with current durable member
// facts. A mismatch is an honest unknown result, never a guessed recovery.
type InitiativeHostReconciler struct {
	store             InitiativeHostRecoveryStore
	host              InitiativeHostRollupSource
	serviceInstanceID string
	newOperationID    func() (string, error)
	clock             Clock
	attemptTimeout    time.Duration
	retryInterval     time.Duration
	logger            BoundaryLogger
}

// NewInitiativeHostReconciler builds the explicit post-connection recovery use case.
func NewInitiativeHostReconciler(config InitiativeHostReconcilerConfig) (*InitiativeHostReconciler, error) {
	if config.Store == nil || config.Host == nil || config.NewOperationID == nil || config.Clock == nil ||
		domain.ValidateAuthorityReference("serviceInstanceId", config.ServiceInstanceID) != nil ||
		config.AttemptTimeout <= 0 || config.AttemptTimeout > time.Minute ||
		config.RetryInterval <= 0 || config.RetryInterval > config.AttemptTimeout {
		return nil, errors.New("create initiative host reconciler: configuration is invalid")
	}
	return &InitiativeHostReconciler{
		store: config.Store, host: config.Host, serviceInstanceID: config.ServiceInstanceID,
		newOperationID: config.NewOperationID, clock: config.Clock,
		attemptTimeout: config.AttemptTimeout, retryInterval: config.RetryInterval, logger: config.Logger,
	}, nil
}

// Reconcile attempts every bound unknown initiative owned entirely by this
// service instance. Host unavailability or disagreement preserves unknown and
// does not prevent unrelated groups from being checked.
func (reconciler *InitiativeHostReconciler) Reconcile(
	ctx context.Context,
) (InitiativeHostReconciliation, error) {
	if ctx == nil {
		return InitiativeHostReconciliation{}, errors.New("reconcile initiatives with host: context is required")
	}
	if err := ctx.Err(); err != nil {
		return InitiativeHostReconciliation{}, err
	}
	initiatives, err := reconciler.store.ListInitiatives(ctx)
	if err != nil {
		return InitiativeHostReconciliation{}, fmt.Errorf("reconcile initiatives with host: list initiatives: %w", err)
	}
	result := InitiativeHostReconciliation{}
	for _, listed := range initiatives {
		if listed.State != domain.InitiativeUnknown || listed.ManagedRunGroupID == "" {
			continue
		}
		initiative, tasks, _, observationErr := reconciler.store.InitiativeObservation(ctx, listed.Handle)
		if observationErr != nil {
			return result, fmt.Errorf("reconcile initiatives with host: read initiative observation: %w", observationErr)
		}
		if initiative.State != domain.InitiativeUnknown || initiative.ManagedRunGroupID == "" ||
			!tasksBelongToService(tasks, reconciler.serviceInstanceID) {
			continue
		}
		result.Attempted++
		operationID, operationErr := reconciler.newOperationID()
		if operationErr != nil || domain.ValidateOperationID(operationID) != nil {
			result.PreservedUnknown++
			reconciler.record(operationID, BoundaryFailed)
			continue
		}
		attemptContext, cancel := context.WithTimeout(ctx, reconciler.attemptTimeout)
		request := InitiativeHostRollupRequest{
			OperationID: operationID, ManagedRunGroupID: initiative.ManagedRunGroupID,
		}
		rollup, readErr := reconciler.host.ReadInitiativeHostRollup(attemptContext, request)
		if readErr == nil && !initiativeHostEvidenceMatches(initiative, tasks, rollup) {
			pendingEgress, pendingErr := reconciler.store.InitiativeHasPendingComisEgress(ctx, initiative.Handle)
			if pendingErr != nil {
				cancel()
				return result, fmt.Errorf("reconcile initiatives with host: read pending Comis egress: %w", pendingErr)
			}
			for pendingEgress && !initiativeHostEvidenceMatches(initiative, tasks, rollup) {
				if waitErr := waitInitiativeHostRetry(attemptContext, reconciler.retryInterval); waitErr != nil {
					if ctx.Err() != nil {
						cancel()
						return result, ctx.Err()
					}
					break
				}
				refreshed, refreshedTasks, _, refreshErr := reconciler.store.InitiativeObservation(
					attemptContext, initiative.Handle,
				)
				if refreshErr != nil {
					cancel()
					return result, fmt.Errorf("reconcile initiatives with host: refresh initiative observation: %w", refreshErr)
				}
				if refreshed.State != domain.InitiativeUnknown ||
					refreshed.ManagedRunGroupID != request.ManagedRunGroupID ||
					!tasksBelongToService(refreshedTasks, reconciler.serviceInstanceID) {
					break
				}
				initiative, tasks = refreshed, refreshedTasks
				rollup, readErr = reconciler.host.ReadInitiativeHostRollup(attemptContext, request)
				if readErr != nil {
					continue
				}
			}
		}
		cancel()
		if readErr != nil || !initiativeHostEvidenceMatches(initiative, tasks, rollup) {
			result.PreservedUnknown++
			reconciler.record(operationID, BoundaryFailed)
			continue
		}
		_, commitErr := reconciler.store.CommitInitiativeHostRecovery(ctx, InitiativeHostRecoveryMutation{
			InitiativeHandle: initiative.Handle, ServiceInstanceID: reconciler.serviceInstanceID,
			ManagedRunGroupID:   initiative.ManagedRunGroupID,
			MemberManagedRunIDs: append([]string(nil), rollup.MemberManagedRunIDs...),
			StateCounts:         rollup.StateCounts, ExpectedStateVersion: initiative.StateVersion,
			At: reconciler.clock().UTC(),
		})
		if errors.Is(commitErr, ErrPrecondition) {
			result.PreservedUnknown++
			reconciler.record(operationID, BoundaryFailed)
			continue
		}
		if commitErr != nil {
			return result, fmt.Errorf("reconcile initiatives with host: commit exact recovery: %w", commitErr)
		}
		result.Recovered++
		reconciler.record(operationID, BoundaryCompleted)
	}
	return result, nil
}

func waitInitiativeHostRetry(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (reconciler *InitiativeHostReconciler) record(operationID string, outcome BoundaryOutcome) {
	record := BoundaryRecord{
		Boundary: BoundaryControl, Operation: "startup_group_reconciliation",
		OperationID: operationID, Outcome: outcome,
	}
	if outcome == BoundaryFailed {
		record.ErrorKind = domain.ErrorPrecondition
		record.Hint = "compare the exact managed-run group rollup with durable initiative member states before recovery"
		record.FailureCause = BoundaryFailureInitiativeHostProjectionMismatch
	}
	RecordBoundary(reconciler.logger, record)
}

func tasksBelongToService(tasks []domain.Task, serviceInstanceID string) bool {
	if len(tasks) == 0 {
		return false
	}
	for _, task := range tasks {
		if task.ServiceInstanceID != serviceInstanceID {
			return false
		}
	}
	return true
}

func initiativeHostEvidenceMatches(
	initiative domain.DevelopmentInitiative,
	tasks []domain.Task,
	rollup InitiativeHostRollup,
) bool {
	if initiative.ManagedRunGroupID != rollup.ManagedRunGroupID || rollup.Validate() != nil {
		return false
	}
	wantIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		wantIDs = append(wantIDs, task.ManagedRunID)
	}
	if !sameManagedRunIdentities(wantIDs, rollup.MemberManagedRunIDs) {
		return false
	}
	wantCounts, err := InitiativeHostStateCountsForTasks(tasks)
	return err == nil && wantCounts == rollup.StateCounts
}

// Validate checks the host projection independently of local initiative facts.
func (rollup InitiativeHostRollup) Validate() error {
	if domain.ValidateAuthorityReference("managedRunGroupId", rollup.ManagedRunGroupID) != nil ||
		!validManagedRunIdentities(rollup.MemberManagedRunIDs) ||
		!rollup.StateCounts.validForMembers(len(rollup.MemberManagedRunIDs)) ||
		rollup.AttentionCount < 0 || rollup.AttentionCount > len(rollup.MemberManagedRunIDs) ||
		rollup.ActiveCustodyCount < 0 || rollup.ActiveCustodyCount > len(rollup.MemberManagedRunIDs) ||
		rollup.UpdatedAtMs < 0 {
		return ErrInvalidInput
	}
	return nil
}

func (counts InitiativeHostStateCounts) validForMembers(memberCount int) bool {
	values := []int{
		counts.Preparing, counts.Active, counts.Waiting, counts.Paused,
		counts.CandidateComplete, counts.Succeeded, counts.Failed, counts.Cancelled, counts.Unknown,
	}
	total := 0
	for _, value := range values {
		if value < 0 {
			return false
		}
		total += value
	}
	return memberCount >= 1 && memberCount <= 16 && total == memberCount
}

func validManagedRunIdentities(identities []string) bool {
	if len(identities) < 1 || len(identities) > 16 {
		return false
	}
	seen := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if domain.ValidateAuthorityReference("managedRunId", identity) != nil {
			return false
		}
		if _, exists := seen[identity]; exists {
			return false
		}
		seen[identity] = struct{}{}
	}
	return true
}

func sameManagedRunIdentities(left, right []string) bool {
	if len(left) != len(right) || !validManagedRunIdentities(left) || !validManagedRunIdentities(right) {
		return false
	}
	left = append([]string(nil), left...)
	right = append([]string(nil), right...)
	sort.Strings(left)
	sort.Strings(right)
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// InitiativeHostStateCountsForTasks maps every durable task state onto the
// smaller host group vocabulary without treating an unknown state as idle.
func InitiativeHostStateCountsForTasks(tasks []domain.Task) (InitiativeHostStateCounts, error) {
	counts := InitiativeHostStateCounts{}
	for _, task := range tasks {
		switch task.State {
		case domain.TaskPrepared:
			counts.Preparing++
		case domain.TaskReady, domain.TaskLaunching, domain.TaskWorking:
			counts.Active++
		case domain.TaskAwaitingDecision, domain.TaskBlocked:
			counts.Waiting++
		case domain.TaskPaused:
			counts.Paused++
		case domain.TaskReconciling, domain.TaskUnknown:
			counts.Unknown++
		case domain.TaskValidating, domain.TaskCandidateComplete, domain.TaskDelivering:
			counts.CandidateComplete++
		case domain.TaskDelivered, domain.TaskCleanupHeld, domain.TaskCleaned:
			counts.Succeeded++
		case domain.TaskFailed:
			counts.Failed++
		case domain.TaskCancelled:
			counts.Cancelled++
		default:
			return InitiativeHostStateCounts{}, errors.New("map initiative member state to host: state is invalid")
		}
	}
	return counts, nil
}
