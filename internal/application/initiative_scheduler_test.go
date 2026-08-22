package application

import (
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeSchedulerAllocatesCapacityInFairInitiativeRounds(t *testing.T) {
	first := schedulingInitiative("initiative-first", time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-first-a", "task-first-b"}, nil, "")
	second := schedulingInitiative("initiative-second", time.Unix(1_800_000_001, 0).UTC(),
		[]string{"task-second-a", "task-second-b"}, nil, "")
	tasks := []domain.Task{
		schedulingTask(t, "task-first-a", domain.TaskReady, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-first-b", domain.TaskReady, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-second-a", domain.TaskReady, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-second-b", domain.TaskReady, "repo-primary", "codex-reviewed"),
	}

	schedules, err := ScheduleInitiatives([]domain.DevelopmentInitiative{second, first}, tasks, InitiativeSchedulingLimits{
		MaxConcurrentTasks: 2, MaxConcurrentTasksPerRepository: 2,
		WorkerProfileLimits: map[string]int{"codex-reviewed": 2},
	})
	if err != nil {
		t.Fatalf("ScheduleInitiatives() error = %v", err)
	}
	launchable := map[string]bool{}
	for _, schedule := range schedules {
		for _, decision := range schedule.Tasks {
			launchable[decision.TaskHandle] = decision.Launchable
		}
	}
	if !launchable["task-first-a"] || !launchable["task-second-a"] {
		t.Fatalf("launchable = %#v, want one oldest task from each initiative", launchable)
	}
	if launchable["task-first-b"] || launchable["task-second-b"] {
		t.Fatalf("launchable = %#v, want later members resource queued", launchable)
	}
	for _, handle := range []string{"task-first-b", "task-second-b"} {
		if decision := schedulingDecision(t, schedules, handle); decision.Reason != ScheduleResourceQueued {
			t.Fatalf("%s reason = %q, want %q", handle, decision.Reason, ScheduleResourceQueued)
		}
	}
}

func TestInitiativeSchedulerUsesClosedDependencyAndContractReasons(t *testing.T) {
	edges := []domain.InitiativeEdge{
		{FromTaskHandle: "task-contract", ToTaskHandle: "task-consumer", Kind: domain.EdgeConsumesArtifact, RequiredArtifactKind: domain.ArtifactAPISchema},
		{FromTaskHandle: "task-contract", ToTaskHandle: "task-dependent", Kind: domain.EdgeBlocksStart},
		{FromTaskHandle: "task-contract", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
	}
	initiative := schedulingInitiative("initiative-reasons", time.Unix(1_800_000_000, 0).UTC(), []string{
		"task-contract", "task-consumer", "task-dependent", "task-independent", "task-integration",
	}, edges, "task-integration")
	initiative.ContractArtifacts = []string{"artifact-api-v2"}
	tasks := []domain.Task{
		schedulingTask(t, "task-contract", domain.TaskFailed, "repo-primary", "codex-reviewed"),
		schedulingTaskWithContracts(t, "task-consumer", []domain.PinnedContract{{
			ArtifactHandle: "artifact-api-v1", Kind: domain.ArtifactAPISchema,
			ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		}}),
		schedulingTask(t, "task-dependent", domain.TaskReady, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-independent", domain.TaskReady, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-integration", domain.TaskReady, "repo-primary", "codex-reviewed"),
	}

	schedules, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, tasks, InitiativeSchedulingLimits{
		MaxConcurrentTasks: 4, MaxConcurrentTasksPerRepository: 4,
		WorkerProfileLimits: map[string]int{"codex-reviewed": 4},
	})
	if err != nil {
		t.Fatalf("ScheduleInitiatives() error = %v", err)
	}
	wants := map[string]InitiativeScheduleReason{
		"task-consumer":    ScheduleContractStale,
		"task-dependent":   ScheduleDependencyBlocked,
		"task-integration": ScheduleIntegrationHeld,
	}
	for handle, want := range wants {
		decision := schedulingDecision(t, schedules, handle)
		if decision.Launchable || decision.Reason != want {
			t.Fatalf("%s decision = %#v, want held by %q", handle, decision, want)
		}
	}
	if decision := schedulingDecision(t, schedules, "task-independent"); !decision.Launchable || decision.Reason != "" {
		t.Fatalf("independent decision = %#v, want launchable despite sibling failure", decision)
	}
	if schedules[0].State != domain.InitiativeActive {
		t.Fatalf("aggregate state = %q, want active while an unrelated lane can progress", schedules[0].State)
	}
}

func TestInitiativeAggregateRemainsActiveWhileAReadySiblingCanProgress(t *testing.T) {
	edges := []domain.InitiativeEdge{
		{FromTaskHandle: "task-backend", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
		{FromTaskHandle: "task-frontend", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
	}
	initiative := schedulingInitiative(
		"initiative-independent-ready-sibling",
		time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-backend", "task-frontend", "task-integration"},
		edges,
		"task-integration",
	)
	tasks := []domain.Task{
		schedulingTask(t, "task-backend", domain.TaskCandidateComplete, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-frontend", domain.TaskReady, "repo-primary", "claude-reviewed"),
		schedulingTask(t, "task-integration", domain.TaskReady, "repo-primary", "codex-reviewed"),
	}

	state, err := DeriveInitiativeState(initiative, tasks)
	if err != nil {
		t.Fatalf("DeriveInitiativeState() error = %v", err)
	}
	if state != domain.InitiativeActive {
		t.Fatalf("DeriveInitiativeState() = %q, want %q while a dependency-ready sibling can progress", state, domain.InitiativeActive)
	}
}

func TestInitiativeSchedulerCountsExistingWorkersAgainstEveryCeiling(t *testing.T) {
	initiative := schedulingInitiative("initiative-queued", time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-queued"}, nil, "")
	tasks := []domain.Task{
		schedulingTask(t, "task-queued", domain.TaskReady, "repo-primary", "codex-reviewed"),
		schedulingTask(t, "task-standalone", domain.TaskWorking, "repo-primary", "codex-reviewed"),
	}
	schedules, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, tasks, InitiativeSchedulingLimits{
		MaxConcurrentTasks: 8, MaxConcurrentTasksPerRepository: 1,
		WorkerProfileLimits: map[string]int{"codex-reviewed": 8},
	})
	if err != nil {
		t.Fatalf("ScheduleInitiatives() error = %v", err)
	}
	decision := schedulingDecision(t, schedules, "task-queued")
	if decision.Launchable || decision.Reason != ScheduleResourceQueued {
		t.Fatalf("queued decision = %#v", decision)
	}
}

func TestInitiativeSchedulerDerivesTerminalAndUnknownAggregateStates(t *testing.T) {
	tests := []struct {
		name  string
		state domain.TaskState
		want  domain.InitiativeState
	}{
		{name: "unknown member", state: domain.TaskUnknown, want: domain.InitiativeUnknown},
		{name: "validating member", state: domain.TaskValidating, want: domain.InitiativeValidating},
		{name: "candidate member", state: domain.TaskCandidateComplete, want: domain.InitiativeCandidateComplete},
		{name: "delivered member", state: domain.TaskDelivered, want: domain.InitiativeDelivered},
		{name: "cancelled member", state: domain.TaskCancelled, want: domain.InitiativeCancelled},
		{name: "failed member", state: domain.TaskFailed, want: domain.InitiativeFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			initiative := schedulingInitiative("initiative-aggregate", time.Unix(1_800_000_000, 0).UTC(),
				[]string{"task-aggregate"}, nil, "")
			schedules, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{
				schedulingTask(t, "task-aggregate", test.state, "repo-primary", "codex-reviewed"),
			}, InitiativeSchedulingLimits{
				MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 1,
				WorkerProfileLimits: map[string]int{"codex-reviewed": 1},
			})
			if err != nil {
				t.Fatalf("ScheduleInitiatives() error = %v", err)
			}
			if schedules[0].State != test.want {
				t.Fatalf("aggregate state = %q, want %q", schedules[0].State, test.want)
			}
		})
	}
}

func TestInitiativeSchedulerNeverReactivatesAnUnknownInitiative(t *testing.T) {
	initiative := schedulingInitiative("initiative-recovery", time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-recovery"}, nil, "")
	initiative.State = domain.InitiativeUnknown
	schedules, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{
		schedulingTask(t, "task-recovery", domain.TaskReady, "repo-primary", "codex-reviewed"),
	}, InitiativeSchedulingLimits{
		MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 1,
		WorkerProfileLimits: map[string]int{"codex-reviewed": 1},
	})
	if err != nil {
		t.Fatalf("ScheduleInitiatives() error = %v", err)
	}
	if schedules[0].State != domain.InitiativeUnknown || schedules[0].Tasks[0].Launchable {
		t.Fatalf("unknown recovery schedule = %#v, want non-launchable unknown", schedules[0])
	}
}

func TestInitiativeSchedulerRefusesIncompleteOrOverlappingAuthority(t *testing.T) {
	initiative := schedulingInitiative("initiative-invalid", time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-member"}, nil, "")
	other := schedulingInitiative("initiative-other", time.Unix(1_800_000_001, 0).UTC(),
		[]string{"task-member"}, nil, "")
	limits := InitiativeSchedulingLimits{
		MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 1,
		WorkerProfileLimits: map[string]int{"codex-reviewed": 1},
	}
	if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, nil, limits); err == nil {
		t.Fatal("schedule without a durable member task succeeded")
	}
	if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative, other}, []domain.Task{
		schedulingTask(t, "task-member", domain.TaskReady, "repo-primary", "codex-reviewed"),
	}, limits); err == nil {
		t.Fatal("task owned by two initiatives was scheduled")
	}
}

func TestInitiativeSchedulerRejectsInvalidLimitsTasksAndMembership(t *testing.T) {
	initiative := schedulingInitiative("initiative-validation", time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-member"}, nil, "")
	task := schedulingTask(t, "task-member", domain.TaskReady, "repo-primary", "codex-reviewed")
	valid := InitiativeSchedulingLimits{
		MaxConcurrentTasks: 2, MaxConcurrentTasksPerRepository: 1,
		WorkerProfileLimits: map[string]int{"codex-reviewed": 2},
	}
	invalidLimits := []InitiativeSchedulingLimits{
		{},
		{MaxConcurrentTasks: 1, MaxConcurrentTasksPerRepository: 2, WorkerProfileLimits: map[string]int{"codex-reviewed": 1}},
		{MaxConcurrentTasks: 2, MaxConcurrentTasksPerRepository: 1, WorkerProfileLimits: map[string]int{"../profile": 1}},
		{MaxConcurrentTasks: 2, MaxConcurrentTasksPerRepository: 1, WorkerProfileLimits: map[string]int{"codex-reviewed": 3}},
	}
	for _, limits := range invalidLimits {
		if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{task}, limits); err == nil {
			t.Fatalf("ScheduleInitiatives(invalid limits %#v) error = nil", limits)
		}
	}
	invalidTask := task
	invalidTask.State = domain.TaskState("invented")
	if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{invalidTask}, valid); err == nil {
		t.Fatal("ScheduleInitiatives(invalid task) error = nil")
	}
	if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{task, task}, valid); err == nil {
		t.Fatal("ScheduleInitiatives(duplicate task) error = nil")
	}
	missingProfile := valid
	missingProfile.WorkerProfileLimits = map[string]int{"claude-reviewed": 1}
	if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{task}, missingProfile); err == nil {
		t.Fatal("ScheduleInitiatives(unconfigured task profile) error = nil")
	}
	wrongRepository := task
	wrongRepository.RepositoryID = "repo-other"
	wrongRepository, _ = wrongRepository.PinBriefRevision()
	if _, err := ScheduleInitiatives([]domain.DevelopmentInitiative{initiative}, []domain.Task{wrongRepository}, valid); err == nil {
		t.Fatal("ScheduleInitiatives(member repository mismatch) error = nil")
	}
}

func TestInitiativeAggregateReducerRejectsInexactInputsAndPreservesTerminalTruth(t *testing.T) {
	initiative := schedulingInitiative("initiative-derive", time.Unix(1_800_000_000, 0).UTC(),
		[]string{"task-member"}, nil, "")
	task := schedulingTask(t, "task-member", domain.TaskCleaned, "repo-primary", "codex-reviewed")
	for _, state := range []domain.InitiativeState{domain.InitiativeDelivered, domain.InitiativeFailed, domain.InitiativeCancelled} {
		terminal := initiative
		terminal.State = state
		got, err := DeriveInitiativeState(terminal, []domain.Task{task})
		if err != nil || got != state {
			t.Fatalf("DeriveInitiativeState(%q) = %q, %v", state, got, err)
		}
	}
	invalidInitiative := initiative
	invalidInitiative.State = domain.InitiativeState("invented")
	if _, err := DeriveInitiativeState(invalidInitiative, []domain.Task{task}); err == nil {
		t.Fatal("DeriveInitiativeState(invalid initiative) error = nil")
	}
	invalidTask := task
	invalidTask.State = domain.TaskState("invented")
	if _, err := DeriveInitiativeState(initiative, []domain.Task{invalidTask}); err == nil {
		t.Fatal("DeriveInitiativeState(invalid task) error = nil")
	}
	if _, err := DeriveInitiativeState(initiative, []domain.Task{task, task}); err == nil {
		t.Fatal("DeriveInitiativeState(duplicate member) error = nil")
	}
	extra := schedulingTask(t, "task-extra", domain.TaskReady, "repo-primary", "codex-reviewed")
	if _, err := DeriveInitiativeState(initiative, []domain.Task{task, extra}); err == nil {
		t.Fatal("DeriveInitiativeState(extra member) error = nil")
	}
}

func TestInitiativeAggregateReducerCoversClosedIntermediateStates(t *testing.T) {
	for _, test := range []struct {
		name            string
		initiativeState domain.InitiativeState
		taskState       domain.TaskState
		owner           bool
		want            domain.InitiativeState
	}{
		{name: "preparing remains preparing", initiativeState: domain.InitiativePreparing, taskState: domain.TaskPrepared, want: domain.InitiativePreparing},
		{name: "owner launching integrates", initiativeState: domain.InitiativeActive, taskState: domain.TaskLaunching, owner: true, want: domain.InitiativeIntegrating},
		{name: "owner validating validates", initiativeState: domain.InitiativeActive, taskState: domain.TaskValidating, owner: true, want: domain.InitiativeValidating},
		{name: "owner delivering is candidate complete", initiativeState: domain.InitiativeActive, taskState: domain.TaskDelivering, owner: true, want: domain.InitiativeCandidateComplete},
		{name: "blocked member blocks", initiativeState: domain.InitiativeActive, taskState: domain.TaskBlocked, want: domain.InitiativeBlocked},
		{name: "reconciling member is unknown", initiativeState: domain.InitiativeActive, taskState: domain.TaskReconciling, want: domain.InitiativeUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			initiative := schedulingInitiative("initiative-intermediate", time.Unix(1_800_000_000, 0).UTC(), []string{"task-member"}, nil, "")
			initiative.State = test.initiativeState
			if test.owner {
				initiative.IntegrationOwnerTask = "task-member"
			}
			got, err := DeriveInitiativeState(initiative, []domain.Task{
				schedulingTask(t, "task-member", test.taskState, "repo-primary", "codex-reviewed"),
			})
			if err != nil || got != test.want {
				t.Fatalf("DeriveInitiativeState() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func schedulingInitiative(
	handle string,
	createdAt time.Time,
	tasks []string,
	edges []domain.InitiativeEdge,
	integrationOwner string,
) domain.DevelopmentInitiative {
	components := make([]domain.InitiativeComponent, 0, len(tasks))
	for _, taskHandle := range tasks {
		components = append(components, domain.InitiativeComponent{
			ComponentHandle: "component-" + taskHandle,
			RepositoryID:    "repo-primary", TaskHandles: []string{taskHandle},
		})
	}
	return domain.DevelopmentInitiative{
		SchemaVersion: 1, Handle: handle, ManagedRunGroupID: "managed-run-group-" + handle,
		State: domain.InitiativeActive,
		BaseRevisionSet: []domain.InitiativeBaseRevision{{
			RepositoryID: "repo-primary", Revision: "0123456789abcdef0123456789abcdef01234567",
		}},
		Components: components, Edges: edges, IntegrationPolicyID: "integration-default",
		IntegrationOwnerTask: integrationOwner, StateVersion: 1,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}
}

func schedulingTask(
	t *testing.T,
	handle string,
	state domain.TaskState,
	repositoryID string,
	profileID string,
) domain.Task {
	t.Helper()
	task := queryTask(handle, state, 1)
	task.RepositoryID = repositoryID
	task.WorkerProfileID = profileID
	updated, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision(%s) error = %v", handle, err)
	}
	return updated
}

func schedulingTaskWithContracts(t *testing.T, handle string, contracts []domain.PinnedContract) domain.Task {
	t.Helper()
	task := schedulingTask(t, handle, domain.TaskReady, "repo-primary", "codex-reviewed")
	task.ConsumedContracts = contracts
	updated, err := task.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision(%s contracts) error = %v", handle, err)
	}
	return updated
}

func schedulingDecision(
	t *testing.T,
	schedules []InitiativeSchedule,
	taskHandle string,
) InitiativeTaskSchedule {
	t.Helper()
	for _, schedule := range schedules {
		for _, decision := range schedule.Tasks {
			if decision.TaskHandle == taskHandle {
				return decision
			}
		}
	}
	t.Fatalf("no schedule decision for %s", taskHandle)
	return InitiativeTaskSchedule{}
}
