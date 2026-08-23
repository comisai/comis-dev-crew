package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type initiativeHostRecoveryStoreStub struct {
	initiatives    []domain.DevelopmentInitiative
	observations   map[string][]domain.Task
	pendingEgress  map[string]bool
	pendingCalls   []string
	commits        []InitiativeHostRecoveryMutation
	listErr        error
	observationErr error
	commitErr      error
}

func (store *initiativeHostRecoveryStoreStub) InitiativeHasPendingComisEgress(
	_ context.Context,
	handle string,
) (bool, error) {
	store.pendingCalls = append(store.pendingCalls, handle)
	return store.pendingEgress[handle], nil
}

func (store *initiativeHostRecoveryStoreStub) ListInitiatives(
	_ context.Context,
) ([]domain.DevelopmentInitiative, error) {
	if store.listErr != nil {
		return nil, store.listErr
	}
	return append([]domain.DevelopmentInitiative(nil), store.initiatives...), nil
}

func (store *initiativeHostRecoveryStoreStub) InitiativeObservation(
	_ context.Context,
	handle string,
) (domain.DevelopmentInitiative, []domain.Task, int64, error) {
	if store.observationErr != nil {
		return domain.DevelopmentInitiative{}, nil, 0, store.observationErr
	}
	for _, initiative := range store.initiatives {
		if initiative.Handle == handle {
			return initiative, append([]domain.Task(nil), store.observations[handle]...), initiative.StateVersion, nil
		}
	}
	return domain.DevelopmentInitiative{}, nil, 0, ErrNotFound
}

func (store *initiativeHostRecoveryStoreStub) CommitInitiativeHostRecovery(
	_ context.Context,
	mutation InitiativeHostRecoveryMutation,
) (domain.DevelopmentInitiative, error) {
	store.commits = append(store.commits, mutation)
	if store.commitErr != nil {
		return domain.DevelopmentInitiative{}, store.commitErr
	}
	for _, initiative := range store.initiatives {
		if initiative.Handle == mutation.InitiativeHandle {
			initiative.State = domain.InitiativeActive
			return initiative, nil
		}
	}
	return domain.DevelopmentInitiative{}, ErrNotFound
}

type initiativeHostRollupSourceStub struct {
	results   map[string]InitiativeHostRollup
	sequences map[string][]InitiativeHostRollup
	errors    map[string]error
	calls     []InitiativeHostRollupRequest
}

func (source *initiativeHostRollupSourceStub) ReadInitiativeHostRollup(
	_ context.Context,
	request InitiativeHostRollupRequest,
) (InitiativeHostRollup, error) {
	source.calls = append(source.calls, request)
	if err := source.errors[request.ManagedRunGroupID]; err != nil {
		return InitiativeHostRollup{}, err
	}
	if sequence := source.sequences[request.ManagedRunGroupID]; len(sequence) > 0 {
		result := sequence[0]
		source.sequences[request.ManagedRunGroupID] = sequence[1:]
		return result, nil
	}
	return source.results[request.ManagedRunGroupID], nil
}

func TestInitiativeHostReconcilerRecoversOnlyExactCurrentServiceGroups(t *testing.T) {
	now := time.Date(2026, time.August, 22, 12, 0, 0, 0, time.UTC)
	current := hostRecoveryInitiative(t, "initiative-current", "service-instance-current", now)
	foreign := hostRecoveryInitiative(t, "initiative-foreign", "service-instance-foreign", now)
	terminal := hostRecoveryInitiative(t, "initiative-terminal", "service-instance-current", now)
	terminal.initiative.State = domain.InitiativeDelivered
	unbound := hostRecoveryInitiative(t, "initiative-unbound", "service-instance-current", now)
	unbound.initiative.ManagedRunGroupID = ""
	store := &initiativeHostRecoveryStoreStub{
		initiatives: []domain.DevelopmentInitiative{
			current.initiative, foreign.initiative, terminal.initiative, unbound.initiative,
		},
		observations: map[string][]domain.Task{
			current.initiative.Handle:  current.tasks,
			foreign.initiative.Handle:  foreign.tasks,
			terminal.initiative.Handle: terminal.tasks,
			unbound.initiative.Handle:  unbound.tasks,
		},
	}
	source := &initiativeHostRollupSourceStub{results: map[string]InitiativeHostRollup{
		current.initiative.ManagedRunGroupID: {
			ManagedRunGroupID: current.initiative.ManagedRunGroupID,
			MemberManagedRunIDs: []string{
				current.tasks[1].ManagedRunID, current.tasks[0].ManagedRunID,
			},
			StateCounts: InitiativeHostStateCounts{Active: 2},
			UpdatedAtMs: now.UnixMilli(),
		},
	}}
	reconciler, err := NewInitiativeHostReconciler(InitiativeHostReconcilerConfig{
		Store: store, Host: source, ServiceInstanceID: "service-instance-current",
		NewOperationID: func() (string, error) { return "operation-host-rollup-0001", nil },
		Clock:          func() time.Time { return now.Add(time.Minute) }, AttemptTimeout: time.Second,
		RetryInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewInitiativeHostReconciler() error = %v", err)
	}

	result, err := reconciler.Reconcile(context.Background())

	if err != nil || result.Attempted != 1 || result.Recovered != 1 || result.PreservedUnknown != 0 {
		t.Fatalf("Reconcile() = %#v, %v", result, err)
	}
	if len(source.calls) != 1 || source.calls[0].ManagedRunGroupID != current.initiative.ManagedRunGroupID ||
		source.calls[0].OperationID != "operation-host-rollup-0001" {
		t.Fatalf("host calls = %#v, want only the current service group", source.calls)
	}
	if len(store.commits) != 1 || store.commits[0].InitiativeHandle != current.initiative.Handle ||
		store.commits[0].ServiceInstanceID != "service-instance-current" ||
		store.commits[0].ExpectedStateVersion != current.initiative.StateVersion {
		t.Fatalf("recovery commits = %#v", store.commits)
	}
}

func TestInitiativeHostReconcilerPreservesUnknownWhenHostEvidenceDiffers(t *testing.T) {
	now := time.Date(2026, time.August, 22, 13, 0, 0, 0, time.UTC)
	fixture := hostRecoveryInitiative(t, "initiative-mismatch", "service-instance-current", now)
	tests := []struct {
		name    string
		rollup  InitiativeHostRollup
		hostErr error
	}{
		{
			name: "member identity differs",
			rollup: InitiativeHostRollup{
				ManagedRunGroupID: fixture.initiative.ManagedRunGroupID,
				MemberManagedRunIDs: []string{
					fixture.tasks[0].ManagedRunID, "managed-run-unexpected",
				},
				StateCounts: InitiativeHostStateCounts{Active: 2}, UpdatedAtMs: now.UnixMilli(),
			},
		},
		{
			name: "state counts differ",
			rollup: InitiativeHostRollup{
				ManagedRunGroupID: fixture.initiative.ManagedRunGroupID,
				MemberManagedRunIDs: []string{
					fixture.tasks[0].ManagedRunID, fixture.tasks[1].ManagedRunID,
				},
				StateCounts: InitiativeHostStateCounts{Waiting: 2}, UpdatedAtMs: now.UnixMilli(),
			},
		},
		{name: "host read is unavailable", hostErr: errors.New("host projection unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logger := &capturingBoundaryLogger{}
			store := &initiativeHostRecoveryStoreStub{
				initiatives:  []domain.DevelopmentInitiative{fixture.initiative},
				observations: map[string][]domain.Task{fixture.initiative.Handle: fixture.tasks},
			}
			source := &initiativeHostRollupSourceStub{
				results: map[string]InitiativeHostRollup{fixture.initiative.ManagedRunGroupID: test.rollup},
				errors:  map[string]error{fixture.initiative.ManagedRunGroupID: test.hostErr},
			}
			reconciler, err := NewInitiativeHostReconciler(InitiativeHostReconcilerConfig{
				Store: store, Host: source, ServiceInstanceID: "service-instance-current",
				NewOperationID: func() (string, error) { return "operation-host-rollup-0002", nil },
				Clock:          func() time.Time { return now.Add(time.Minute) }, AttemptTimeout: time.Second,
				RetryInterval: time.Millisecond, Logger: logger,
			})
			if err != nil {
				t.Fatalf("NewInitiativeHostReconciler() error = %v", err)
			}

			result, err := reconciler.Reconcile(context.Background())

			if err != nil || result.Attempted != 1 || result.Recovered != 0 || result.PreservedUnknown != 1 {
				t.Fatalf("Reconcile() = %#v, %v", result, err)
			}
			if len(store.commits) != 0 {
				t.Fatalf("mismatched host evidence committed recovery: %#v", store.commits)
			}
			if test.name == "state counts differ" {
				if len(logger.records) != 1 {
					t.Fatalf("boundary records = %#v, want one mismatch", logger.records)
				}
				record := reflect.ValueOf(logger.records[0])
				for field, want := range map[string]any{
					"InitiativeHandle":        fixture.initiative.Handle,
					"ManagedRunGroupID":       fixture.initiative.ManagedRunGroupID,
					"AttemptCount":            1,
					"HostProjectionMismatch":  "state_counts",
					"ExpectedHostStateCounts": InitiativeHostStateCounts{Active: 2},
					"ObservedHostStateCounts": InitiativeHostStateCounts{Waiting: 2},
				} {
					got := record.FieldByName(field)
					if !got.IsValid() || !reflect.DeepEqual(got.Interface(), want) {
						t.Errorf("boundary record field %s = %v, want %#v", field, got, want)
					}
				}
			}
		})
	}
}

func TestInitiativeHostReconcilerWaitsForPendingEgressToSettle(t *testing.T) {
	now := time.Date(2026, time.August, 23, 4, 0, 0, 0, time.UTC)
	fixture := hostRecoveryInitiative(t, "initiative-pending-egress", "service-instance-current", now)
	fixture.tasks[0].State = domain.TaskCandidateComplete
	store := &initiativeHostRecoveryStoreStub{
		initiatives:   []domain.DevelopmentInitiative{fixture.initiative},
		observations:  map[string][]domain.Task{fixture.initiative.Handle: fixture.tasks},
		pendingEgress: map[string]bool{fixture.initiative.Handle: true},
	}
	exact := InitiativeHostRollup{
		ManagedRunGroupID: fixture.initiative.ManagedRunGroupID,
		MemberManagedRunIDs: []string{
			fixture.tasks[0].ManagedRunID, fixture.tasks[1].ManagedRunID,
		},
		StateCounts: InitiativeHostStateCounts{Active: 1, CandidateComplete: 1},
		UpdatedAtMs: now.Add(time.Second).UnixMilli(),
	}
	source := &initiativeHostRollupSourceStub{sequences: map[string][]InitiativeHostRollup{
		fixture.initiative.ManagedRunGroupID: {
			{
				ManagedRunGroupID: fixture.initiative.ManagedRunGroupID,
				MemberManagedRunIDs: []string{
					fixture.tasks[0].ManagedRunID, fixture.tasks[1].ManagedRunID,
				},
				StateCounts: InitiativeHostStateCounts{Active: 2}, UpdatedAtMs: now.UnixMilli(),
			},
			exact,
		},
	}}
	reconciler, err := NewInitiativeHostReconciler(InitiativeHostReconcilerConfig{
		Store: store, Host: source, ServiceInstanceID: "service-instance-current",
		NewOperationID: func() (string, error) { return "operation-host-rollup-pending", nil },
		Clock:          func() time.Time { return now.Add(2 * time.Second) }, AttemptTimeout: time.Second,
		RetryInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewInitiativeHostReconciler() error = %v", err)
	}

	result, err := reconciler.Reconcile(context.Background())

	if err != nil || result.Attempted != 1 || result.Recovered != 1 || result.PreservedUnknown != 0 {
		t.Fatalf("Reconcile() = %#v, %v", result, err)
	}
	if len(source.calls) != 2 || len(store.pendingCalls) != 1 {
		t.Fatalf("host calls = %d, pending-egress calls = %d, want 2 and 1", len(source.calls), len(store.pendingCalls))
	}
	if len(store.commits) != 1 || store.commits[0].StateCounts != exact.StateCounts {
		t.Fatalf("recovery commits = %#v, want settled exact rollup", store.commits)
	}
}

func TestInitiativeHostStateCountsCoverEveryDurableTaskState(t *testing.T) {
	tasks := make([]domain.Task, 0)
	for _, state := range []domain.TaskState{
		domain.TaskPrepared,
		domain.TaskReady, domain.TaskLaunching, domain.TaskWorking,
		domain.TaskAwaitingDecision, domain.TaskBlocked,
		domain.TaskPaused,
		domain.TaskReconciling, domain.TaskUnknown,
		domain.TaskValidating, domain.TaskCandidateComplete, domain.TaskDelivering,
		domain.TaskDelivered, domain.TaskCleanupHeld, domain.TaskCleaned,
		domain.TaskFailed,
		domain.TaskCancelled,
	} {
		tasks = append(tasks, domain.Task{State: state})
	}

	got, err := InitiativeHostStateCountsForTasks(tasks)
	want := InitiativeHostStateCounts{
		Preparing: 1, Active: 3, Waiting: 2, Paused: 1, Unknown: 2,
		CandidateComplete: 3, Succeeded: 3, Failed: 1, Cancelled: 1,
	}

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("InitiativeHostStateCountsForTasks() = %#v, %v, want %#v", got, err, want)
	}
	if _, err := InitiativeHostStateCountsForTasks([]domain.Task{{State: "invented"}}); err == nil {
		t.Fatal("InitiativeHostStateCountsForTasks(unknown state) error = nil")
	}
}

func TestInitiativeHostReconcilerRejectsInvalidConfigurationAndDurableFailures(t *testing.T) {
	if _, err := NewInitiativeHostReconciler(InitiativeHostReconcilerConfig{}); err == nil {
		t.Fatal("NewInitiativeHostReconciler(empty) error = nil")
	}
	now := time.Date(2026, time.August, 22, 14, 0, 0, 0, time.UTC)
	fixture := hostRecoveryInitiative(t, "initiative-errors", "service-instance-current", now)
	exactRollup := InitiativeHostRollup{
		ManagedRunGroupID: fixture.initiative.ManagedRunGroupID,
		MemberManagedRunIDs: []string{
			fixture.tasks[0].ManagedRunID, fixture.tasks[1].ManagedRunID,
		},
		StateCounts: InitiativeHostStateCounts{Active: 2}, UpdatedAtMs: now.UnixMilli(),
	}
	newReconciler := func(store *initiativeHostRecoveryStoreStub, operationErr error) *InitiativeHostReconciler {
		t.Helper()
		reconciler, err := NewInitiativeHostReconciler(InitiativeHostReconcilerConfig{
			Store: store,
			Host: &initiativeHostRollupSourceStub{results: map[string]InitiativeHostRollup{
				fixture.initiative.ManagedRunGroupID: exactRollup,
			}},
			ServiceInstanceID: "service-instance-current",
			NewOperationID: func() (string, error) {
				return "operation-host-rollup-errors", operationErr
			},
			Clock: func() time.Time { return now.Add(time.Minute) }, AttemptTimeout: time.Second,
			RetryInterval: time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewInitiativeHostReconciler() error = %v", err)
		}
		return reconciler
	}
	baseStore := func() *initiativeHostRecoveryStoreStub {
		return &initiativeHostRecoveryStoreStub{
			initiatives:  []domain.DevelopmentInitiative{fixture.initiative},
			observations: map[string][]domain.Task{fixture.initiative.Handle: fixture.tasks},
		}
	}

	var nilContext context.Context
	if _, err := newReconciler(baseStore(), nil).Reconcile(nilContext); err == nil {
		t.Fatal("Reconcile(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newReconciler(baseStore(), nil).Reconcile(cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Reconcile(cancelled) error = %v", err)
	}

	listFailure := baseStore()
	listFailure.listErr = errors.New("list unavailable")
	if _, err := newReconciler(listFailure, nil).Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile(list failure) error = nil")
	}
	observationFailure := baseStore()
	observationFailure.observationErr = errors.New("observation unavailable")
	if _, err := newReconciler(observationFailure, nil).Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile(observation failure) error = nil")
	}

	operationFailure := baseStore()
	result, err := newReconciler(operationFailure, errors.New("entropy unavailable")).Reconcile(context.Background())
	if err != nil || result.PreservedUnknown != 1 || len(operationFailure.commits) != 0 {
		t.Fatalf("Reconcile(operation identity failure) = %#v, %v", result, err)
	}
	preconditionFailure := baseStore()
	preconditionFailure.commitErr = ErrPrecondition
	result, err = newReconciler(preconditionFailure, nil).Reconcile(context.Background())
	if err != nil || result.PreservedUnknown != 1 || len(preconditionFailure.commits) != 1 {
		t.Fatalf("Reconcile(commit precondition) = %#v, %v", result, err)
	}
	durableFailure := baseStore()
	durableFailure.commitErr = errors.New("write unavailable")
	if _, err := newReconciler(durableFailure, nil).Reconcile(context.Background()); err == nil {
		t.Fatal("Reconcile(commit failure) error = nil")
	}
}

type hostRecoveryFixture struct {
	initiative domain.DevelopmentInitiative
	tasks      []domain.Task
}

func hostRecoveryInitiative(
	t *testing.T,
	handle string,
	serviceInstanceID string,
	now time.Time,
) hostRecoveryFixture {
	t.Helper()
	initiative := schedulingInitiative(handle, now, []string{handle + "-a", handle + "-b"}, nil, "")
	initiative.State = domain.InitiativeUnknown
	initiative.StateVersion = 7
	tasks := make([]domain.Task, 0, 2)
	for index, taskHandle := range []string{handle + "-a", handle + "-b"} {
		task := schedulingTask(t, taskHandle, domain.TaskReady, "repo-primary", "codex-reviewed")
		task.ServiceInstanceID = serviceInstanceID
		task.ManagedRunID = "managed-run-" + taskHandle
		task.WorkspaceLeaseID = "workspace-lease-" + taskHandle
		task.StateVersion = int64(8 + index)
		tasks = append(tasks, task)
	}
	return hostRecoveryFixture{initiative: initiative, tasks: tasks}
}
