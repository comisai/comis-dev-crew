package application

import (
	"errors"
	"fmt"
	"sort"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// InitiativeScheduleReason is the closed explanation for a ready member that
// the scheduler did not select. An empty reason means the task is either
// launchable or already outside the ready state.
type InitiativeScheduleReason string

const (
	ScheduleDependencyBlocked InitiativeScheduleReason = "dependency_blocked"
	ScheduleResourceQueued    InitiativeScheduleReason = "resource_queued"
	ScheduleContractStale     InitiativeScheduleReason = "contract_stale"
	ScheduleIntegrationHeld   InitiativeScheduleReason = "integration_held"
)

// InitiativeSchedulingLimits are the three reviewed concurrency ceilings.
// Limits are applied to the whole fleet, not independently per initiative.
type InitiativeSchedulingLimits struct {
	MaxConcurrentTasks              int
	MaxConcurrentTasksPerRepository int
	WorkerProfileLimits             map[string]int
}

// InitiativeTaskSchedule is one deterministic launch decision.
type InitiativeTaskSchedule struct {
	TaskHandle string                   `json:"taskHandle"`
	State      domain.TaskState         `json:"state"`
	Launchable bool                     `json:"launchable"`
	Reason     InitiativeScheduleReason `json:"reason,omitempty"`
}

// InitiativeSchedule is one initiative's aggregate state and member decisions.
type InitiativeSchedule struct {
	InitiativeHandle string                   `json:"initiativeHandle"`
	State            domain.InitiativeState   `json:"state"`
	Tasks            []InitiativeTaskSchedule `json:"tasks"`
}

type schedulingCandidate struct {
	scheduleIndex int
	decisionIndex int
	task          domain.Task
}

type schedulingUsage struct {
	host         int
	repositories map[string]int
	profiles     map[string]int
}

func cloneInitiativeSchedulingLimits(limits InitiativeSchedulingLimits) InitiativeSchedulingLimits {
	cloned := limits
	cloned.WorkerProfileLimits = make(map[string]int, len(limits.WorkerProfileLimits))
	for profileID, limit := range limits.WorkerProfileLimits {
		cloned.WorkerProfileLimits[profileID] = limit
	}
	return cloned
}

// ScheduleInitiatives makes a fleet-wide, deterministic scheduling decision.
// Existing workers consume capacity first. Remaining slots are offered in
// initiative creation order, one member per initiative per round, so a large
// initiative cannot starve a later independent initiative.
func ScheduleInitiatives(
	initiatives []domain.DevelopmentInitiative,
	tasks []domain.Task,
	artifacts []domain.ComponentContractArtifact,
	limits InitiativeSchedulingLimits,
) ([]InitiativeSchedule, error) {
	if err := validateSchedulingLimits(limits); err != nil {
		return nil, err
	}
	ordered := append([]domain.DevelopmentInitiative(nil), initiatives...)
	sort.Slice(ordered, func(left, right int) bool {
		if !ordered[left].CreatedAt.Equal(ordered[right].CreatedAt) {
			return ordered[left].CreatedAt.Before(ordered[right].CreatedAt)
		}
		return ordered[left].Handle < ordered[right].Handle
	})
	tasksByHandle, usage, err := indexSchedulingTasks(tasks, limits)
	if err != nil {
		return nil, err
	}
	artifactsByInitiative, err := indexSchedulingArtifacts(ordered, artifacts)
	if err != nil {
		return nil, err
	}

	schedules := make([]InitiativeSchedule, len(ordered))
	candidates := make([][]schedulingCandidate, len(ordered))
	memberOwner := make(map[string]string)
	for initiativeIndex, initiative := range ordered {
		if err := initiative.Validate(); err != nil {
			return nil, fmt.Errorf("schedule initiative %q: %w", initiative.Handle, err)
		}
		schedule, ready, err := scheduleOneInitiative(
			initiativeIndex, initiative, tasksByHandle, artifactsByInitiative[initiative.Handle], memberOwner,
		)
		if err != nil {
			return nil, err
		}
		schedules[initiativeIndex] = schedule
		candidates[initiativeIndex] = ready
	}

	allocateInitiativeCandidates(schedules, candidates, limits, &usage)
	for index := range schedules {
		schedules[index].State = deriveInitiativeState(ordered[index], schedules[index].Tasks)
	}
	return schedules, nil
}

// DeriveInitiativeState reduces an exact durable member set without applying
// resource capacity. Capacity affects when ready work starts, not whether the
// initiative's persisted lifecycle is active, blocked, or terminal.
func DeriveInitiativeState(
	initiative domain.DevelopmentInitiative,
	members []domain.Task,
	artifacts []domain.ComponentContractArtifact,
) (domain.InitiativeState, error) {
	if err := initiative.Validate(); err != nil {
		return "", fmt.Errorf("derive initiative state: %w", err)
	}
	indexed := make(map[string]domain.Task, len(members))
	for _, task := range members {
		if err := task.Validate(); err != nil {
			return "", fmt.Errorf("derive initiative member %q: %w", task.Handle, err)
		}
		if _, exists := indexed[task.Handle]; exists {
			return "", errors.New("derive initiative state: member handles must be unique")
		}
		indexed[task.Handle] = task
	}
	indexedArtifacts, err := indexSchedulingArtifacts(
		[]domain.DevelopmentInitiative{initiative}, artifacts,
	)
	if err != nil {
		return "", err
	}
	schedule, ready, err := scheduleOneInitiative(
		0, initiative, indexed, indexedArtifacts[initiative.Handle], make(map[string]string),
	)
	if err != nil {
		return "", err
	}
	if len(schedule.Tasks) != len(indexed) {
		return "", errors.New("derive initiative state: member set contains a task outside the initiative")
	}
	// Capacity is deliberately absent from aggregate derivation. Mark only the
	// dependency-ready candidates selected by the scheduler so an independent
	// ready lane remains progress while a sibling is held.
	for _, candidate := range ready {
		schedule.Tasks[candidate.decisionIndex].Launchable = true
	}
	return deriveInitiativeState(initiative, schedule.Tasks), nil
}

func validateSchedulingLimits(limits InitiativeSchedulingLimits) error {
	if limits.MaxConcurrentTasks < 1 || limits.MaxConcurrentTasks > 1024 ||
		limits.MaxConcurrentTasksPerRepository < 1 ||
		limits.MaxConcurrentTasksPerRepository > limits.MaxConcurrentTasks ||
		len(limits.WorkerProfileLimits) == 0 || len(limits.WorkerProfileLimits) > 64 {
		return errors.New("schedule initiatives: concurrency limits are invalid")
	}
	for profileID, limit := range limits.WorkerProfileLimits {
		if domain.ValidateAuthorityReference("workerProfileId", profileID) != nil ||
			limit < 1 || limit > limits.MaxConcurrentTasks {
			return errors.New("schedule initiatives: worker profile limit is invalid")
		}
	}
	return nil
}

func indexSchedulingTasks(
	tasks []domain.Task,
	limits InitiativeSchedulingLimits,
) (map[string]domain.Task, schedulingUsage, error) {
	indexed := make(map[string]domain.Task, len(tasks))
	usage := schedulingUsage{repositories: make(map[string]int), profiles: make(map[string]int)}
	for _, task := range tasks {
		if err := task.Validate(); err != nil {
			return nil, schedulingUsage{}, fmt.Errorf("schedule task %q: %w", task.Handle, err)
		}
		if _, exists := indexed[task.Handle]; exists {
			return nil, schedulingUsage{}, errors.New("schedule initiatives: task handles must be unique")
		}
		if _, configured := limits.WorkerProfileLimits[task.WorkerProfileID]; !configured {
			return nil, schedulingUsage{}, errors.New("schedule initiatives: task worker profile has no concurrency limit")
		}
		indexed[task.Handle] = task
		if taskConsumesWorker(task.State) {
			usage.host++
			usage.repositories[task.RepositoryID]++
			usage.profiles[task.WorkerProfileID]++
		}
	}
	return indexed, usage, nil
}

func scheduleOneInitiative(
	scheduleIndex int,
	initiative domain.DevelopmentInitiative,
	tasksByHandle map[string]domain.Task,
	currentArtifacts map[string]domain.ComponentContractArtifact,
	memberOwner map[string]string,
) (InitiativeSchedule, []schedulingCandidate, error) {
	schedule := InitiativeSchedule{InitiativeHandle: initiative.Handle}
	currentArtifactHandles := make(map[string]struct{}, len(initiative.ContractArtifacts))
	for _, handle := range initiative.ContractArtifacts {
		currentArtifactHandles[handle] = struct{}{}
	}
	for _, component := range initiative.Components {
		for _, handle := range component.TaskHandles {
			if owner, exists := memberOwner[handle]; exists {
				return InitiativeSchedule{}, nil, fmt.Errorf(
					"schedule initiatives: task %q belongs to both %q and %q", handle, owner, initiative.Handle,
				)
			}
			memberOwner[handle] = initiative.Handle
			task, exists := tasksByHandle[handle]
			if !exists {
				return InitiativeSchedule{}, nil, fmt.Errorf("schedule initiative %q: member task %q is missing", initiative.Handle, handle)
			}
			if task.RepositoryID != component.RepositoryID {
				return InitiativeSchedule{}, nil, fmt.Errorf("schedule initiative %q: member repository differs", initiative.Handle)
			}
			schedule.Tasks = append(schedule.Tasks, InitiativeTaskSchedule{TaskHandle: handle, State: task.State})
		}
	}
	sort.Slice(schedule.Tasks, func(left, right int) bool {
		return schedule.Tasks[left].TaskHandle < schedule.Tasks[right].TaskHandle
	})
	candidates := make([]schedulingCandidate, 0, len(schedule.Tasks))
	for decisionIndex := range schedule.Tasks {
		decision := &schedule.Tasks[decisionIndex]
		task := tasksByHandle[decision.TaskHandle]
		if task.State != domain.TaskReady || initiative.State != domain.InitiativeActive {
			continue
		}
		decision.Reason = launchDependencyReason(
			initiative, task, tasksByHandle, currentArtifacts, currentArtifactHandles,
		)
		if decision.Reason == "" {
			candidates = append(candidates, schedulingCandidate{
				scheduleIndex: scheduleIndex, decisionIndex: decisionIndex, task: task,
			})
		}
	}
	return schedule, candidates, nil
}

func launchDependencyReason(
	initiative domain.DevelopmentInitiative,
	task domain.Task,
	tasks map[string]domain.Task,
	currentArtifacts map[string]domain.ComponentContractArtifact,
	currentArtifactHandles map[string]struct{},
) InitiativeScheduleReason {
	for _, edge := range initiative.Edges {
		if edge.ToTaskHandle != task.Handle || edge.Kind != domain.EdgeConsumesArtifact {
			continue
		}
		pinned, current := taskPinsCurrentContract(
			task, edge.FromTaskHandle, edge.RequiredArtifactKind,
			currentArtifacts, currentArtifactHandles,
		)
		if pinned && !current {
			return ScheduleContractStale
		}
		if !pinned {
			return ScheduleDependencyBlocked
		}
	}
	for _, edge := range initiative.Edges {
		if edge.ToTaskHandle != task.Handle || edge.Kind == domain.EdgeBlocksValidation ||
			edge.Kind == domain.EdgeConsumesArtifact {
			continue
		}
		predecessor := tasks[edge.FromTaskHandle]
		if taskDependencySatisfied(predecessor.State) {
			continue
		}
		if edge.Kind == domain.EdgeIntegratesAfter && task.Handle == initiative.IntegrationOwnerTask {
			return ScheduleIntegrationHeld
		}
		return ScheduleDependencyBlocked
	}
	return ""
}

func taskPinsCurrentContract(
	task domain.Task,
	producerTaskHandle string,
	kind domain.ContractArtifactKind,
	currentArtifacts map[string]domain.ComponentContractArtifact,
	currentArtifactHandles map[string]struct{},
) (bool, bool) {
	pinned := false
	for _, contract := range task.ConsumedContracts {
		if contract.Kind != kind {
			continue
		}
		pinned = true
		artifact, recorded := currentArtifacts[contract.ArtifactHandle]
		_, current := currentArtifactHandles[contract.ArtifactHandle]
		if recorded && current && artifact.ProducerTaskHandle == producerTaskHandle &&
			artifact.Kind == contract.Kind && artifact.ContentHash == contract.ContentHash {
			return true, true
		}
	}
	return pinned, false
}

func taskDependencySatisfied(state domain.TaskState) bool {
	return state.SatisfiesInitiativeDependency()
}

func taskConsumesWorker(state domain.TaskState) bool {
	switch state {
	case domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision,
		domain.TaskBlocked, domain.TaskPaused, domain.TaskReconciling, domain.TaskUnknown:
		return true
	default:
		return false
	}
}

func allocateInitiativeCandidates(
	schedules []InitiativeSchedule,
	candidates [][]schedulingCandidate,
	limits InitiativeSchedulingLimits,
	usage *schedulingUsage,
) {
	roundOffsets := make([]int, len(schedules))
	maximumRound := 0
	for initiativeIndex, initiativeCandidates := range candidates {
		roundOffsets[initiativeIndex] = initiativeRoundOffset(schedules[initiativeIndex].Tasks)
		if end := roundOffsets[initiativeIndex] + len(initiativeCandidates); end > maximumRound {
			maximumRound = end
		}
	}
	for round := 0; round < maximumRound; round++ {
		for initiativeIndex := range candidates {
			candidateIndex := round - roundOffsets[initiativeIndex]
			if candidateIndex < 0 || candidateIndex >= len(candidates[initiativeIndex]) {
				continue
			}
			candidate := candidates[initiativeIndex][candidateIndex]
			decision := &schedules[candidate.scheduleIndex].Tasks[candidate.decisionIndex]
			if schedulingCapacityAvailable(candidate.task, limits, *usage) {
				decision.Launchable = true
				usage.host++
				usage.repositories[candidate.task.RepositoryID]++
				usage.profiles[candidate.task.WorkerProfileID]++
				continue
			}
			decision.Reason = ScheduleResourceQueued
		}
	}
}

// A member that left the prepared/ready states already consumed its
// initiative's turn. Keeping that durable progress as the next candidate's
// round offset prevents an older initiative from returning to round zero on
// every scheduling transaction and starving a later initiative.
func initiativeRoundOffset(tasks []InitiativeTaskSchedule) int {
	offset := 0
	for _, task := range tasks {
		if task.State != domain.TaskPrepared && task.State != domain.TaskReady {
			offset++
		}
	}
	return offset
}

func schedulingCapacityAvailable(
	task domain.Task,
	limits InitiativeSchedulingLimits,
	usage schedulingUsage,
) bool {
	return usage.host < limits.MaxConcurrentTasks &&
		usage.repositories[task.RepositoryID] < limits.MaxConcurrentTasksPerRepository &&
		usage.profiles[task.WorkerProfileID] < limits.WorkerProfileLimits[task.WorkerProfileID]
}

func deriveInitiativeState(
	initiative domain.DevelopmentInitiative,
	decisions []InitiativeTaskSchedule,
) domain.InitiativeState {
	switch initiative.State {
	case domain.InitiativePreparing, domain.InitiativeUnknown,
		domain.InitiativeDelivered, domain.InitiativeFailed, domain.InitiativeCancelled:
		return initiative.State
	}
	counts := make(map[domain.TaskState]int)
	for _, decision := range decisions {
		counts[decision.State]++
	}
	if counts[domain.TaskUnknown]+counts[domain.TaskReconciling] > 0 {
		return domain.InitiativeUnknown
	}
	if allTaskStates(decisions, domain.TaskDelivered, domain.TaskCleaned) {
		if initiative.State == domain.InitiativeCancelled || initiative.State == domain.InitiativeFailed {
			return initiative.State
		}
		return domain.InitiativeDelivered
	}
	if allTaskStates(decisions, domain.TaskCancelled, domain.TaskCleaned) {
		return domain.InitiativeCancelled
	}
	ownerState := taskStateInSchedule(decisions, initiative.IntegrationOwnerTask)
	switch ownerState {
	case domain.TaskValidating:
		return domain.InitiativeValidating
	case domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision, domain.TaskPaused:
		return domain.InitiativeIntegrating
	}
	if counts[domain.TaskValidating] > 0 {
		return domain.InitiativeValidating
	}
	if allTaskStates(decisions, domain.TaskCandidateComplete, domain.TaskDelivering,
		domain.TaskDelivered, domain.TaskCleanupHeld, domain.TaskCleaned) {
		return domain.InitiativeCandidateComplete
	}
	if hasInitiativeProgress(decisions) {
		return domain.InitiativeActive
	}
	if counts[domain.TaskFailed] > 0 {
		return domain.InitiativeFailed
	}
	if initiativeScheduleHeld(decisions) || counts[domain.TaskBlocked] > 0 {
		return domain.InitiativeBlocked
	}
	return domain.InitiativeActive
}

func allTaskStates(decisions []InitiativeTaskSchedule, allowed ...domain.TaskState) bool {
	if len(decisions) == 0 {
		return false
	}
	accepted := make(map[domain.TaskState]struct{}, len(allowed))
	for _, state := range allowed {
		accepted[state] = struct{}{}
	}
	for _, decision := range decisions {
		if _, ok := accepted[decision.State]; !ok {
			return false
		}
	}
	return true
}

func taskStateInSchedule(decisions []InitiativeTaskSchedule, handle string) domain.TaskState {
	for _, decision := range decisions {
		if decision.TaskHandle == handle {
			return decision.State
		}
	}
	return ""
}

func hasInitiativeProgress(decisions []InitiativeTaskSchedule) bool {
	for _, decision := range decisions {
		if decision.Launchable || decision.Reason == ScheduleResourceQueued {
			return true
		}
		switch decision.State {
		case domain.TaskLaunching, domain.TaskWorking, domain.TaskAwaitingDecision,
			domain.TaskPaused, domain.TaskDelivering:
			return true
		}
	}
	return false
}

func initiativeScheduleHeld(decisions []InitiativeTaskSchedule) bool {
	for _, decision := range decisions {
		switch decision.Reason {
		case ScheduleDependencyBlocked, ScheduleContractStale, ScheduleIntegrationHeld:
			return true
		}
	}
	return false
}
