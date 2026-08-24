package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

type initiativeBacklogRepository interface {
	CreateInitiative(context.Context, domain.DevelopmentInitiative) error
	GetInitiative(context.Context, string) (domain.DevelopmentInitiative, error)
	ListInitiatives(context.Context) ([]domain.DevelopmentInitiative, error)
	CreateBacklogItem(context.Context, domain.BacklogItem) error
	GetBacklogItem(context.Context, string) (domain.BacklogItem, error)
	ListBacklogItems(context.Context) ([]domain.BacklogItem, error)
}

type initiativeQuerySnapshotRepository interface {
	InitiativeSnapshot(context.Context, application.InitiativeFilter) ([]domain.DevelopmentInitiative, string, int64, error)
	InitiativeObservation(context.Context, string) (domain.DevelopmentInitiative, []domain.Task, int64, error)
	BacklogSnapshot(context.Context, application.BacklogFilter) ([]domain.BacklogItem, string, int64, error)
}

func TestInitiativeAndBacklogRecordsSurviveAnExactStoreRestart(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	repository := requireInitiativeBacklogRepository(t, store)
	initiative := persistenceInitiative("initiative-persist-0002", domain.InitiativePreparing, 7)
	backlog := persistenceBacklogItem("backlog-persist-0002")
	if err := repository.CreateInitiative(ctx, initiative); err != nil {
		t.Fatalf("CreateInitiative() error = %v", err)
	}
	if err := repository.CreateBacklogItem(ctx, backlog); err != nil {
		t.Fatalf("CreateBacklogItem() error = %v", err)
	}
	if err := repository.CreateInitiative(ctx, persistenceInitiative(
		"initiative-persist-0001", domain.InitiativeDelivered, 6,
	)); err != nil {
		t.Fatalf("CreateInitiative(second) error = %v", err)
	}
	if err := repository.CreateBacklogItem(ctx, persistenceBacklogItem("backlog-persist-0001")); err != nil {
		t.Fatalf("CreateBacklogItem(second) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := requireInitiativeBacklogRepository(t, reopened)
	gotInitiative, err := restarted.GetInitiative(ctx, initiative.Handle)
	if err != nil || !reflect.DeepEqual(gotInitiative, initiative) {
		t.Fatalf("GetInitiative(restart) = %#v, %v, want %#v", gotInitiative, err, initiative)
	}
	gotBacklog, err := restarted.GetBacklogItem(ctx, backlog.Handle)
	if err != nil || !reflect.DeepEqual(gotBacklog, backlog) {
		t.Fatalf("GetBacklogItem(restart) = %#v, %v, want %#v", gotBacklog, err, backlog)
	}
	initiatives, err := restarted.ListInitiatives(ctx)
	if err != nil || len(initiatives) != 2 || initiatives[0].Handle != "initiative-persist-0001" ||
		initiatives[1].Handle != "initiative-persist-0002" {
		t.Fatalf("ListInitiatives() = %#v, %v, want deterministic handle order", initiatives, err)
	}
	backlogItems, err := restarted.ListBacklogItems(ctx)
	if err != nil || len(backlogItems) != 2 || backlogItems[0].Handle != "backlog-persist-0001" ||
		backlogItems[1].Handle != "backlog-persist-0002" {
		t.Fatalf("ListBacklogItems() = %#v, %v, want deterministic handle order", backlogItems, err)
	}
}

func TestInitiativeAndBacklogQuerySnapshotsCarryOneDurableVersion(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	member := storeTask("task-component-a", 12)
	member.RepositoryID = "repo-primary"
	member.BaseRevision = "0123456789abcdef0123456789abcdef01234567"
	member.BriefRevisionHash = ""
	member, err = member.PinBriefRevision()
	if err != nil {
		t.Fatalf("PinBriefRevision() error = %v", err)
	}
	if err := store.CreateTask(ctx, member); err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	initiative := persistenceInitiative("initiative-snapshot", domain.InitiativeActive, 12)
	initiative.Components = initiative.Components[:1]
	initiative.Edges = []domain.InitiativeEdge{}
	initiative.IntegrationOwnerTask = "task-component-a"
	if err := store.CreateInitiative(ctx, initiative); err != nil {
		t.Fatalf("CreateInitiative() error = %v", err)
	}
	backlog := persistenceBacklogItem("backlog-snapshot")
	if err := store.CreateBacklogItem(ctx, backlog); err != nil {
		t.Fatalf("CreateBacklogItem() error = %v", err)
	}
	repository, ok := any(store).(initiativeQuerySnapshotRepository)
	if !ok {
		t.Fatal("SQLite Store does not implement initiative query snapshots")
	}

	initiatives, cursor, version, err := repository.InitiativeSnapshot(ctx, application.InitiativeFilter{
		Limit: application.MaximumInitiativePage,
	})
	if err != nil || version != 12 || cursor != "" ||
		len(initiatives) != 1 || initiatives[0].Handle != initiative.Handle {
		t.Fatalf("InitiativeSnapshot() = %#v, %q, %d, %v", initiatives, cursor, version, err)
	}
	gotInitiative, tasks, version, err := repository.InitiativeObservation(ctx, initiative.Handle)
	if err != nil || version != 12 || gotInitiative.Handle != initiative.Handle ||
		len(tasks) != 1 || tasks[0].Handle != member.Handle {
		t.Fatalf("InitiativeObservation() = %#v, %#v, %d, %v", gotInitiative, tasks, version, err)
	}
	items, cursor, version, err := repository.BacklogSnapshot(ctx, application.BacklogFilter{
		Limit: application.MaximumBacklogPage,
	})
	if err != nil || version != 12 || cursor != "" ||
		len(items) != 1 || items[0].Handle != backlog.Handle {
		t.Fatalf("BacklogSnapshot() = %#v, %q, %d, %v", items, cursor, version, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, _, _, err := repository.InitiativeSnapshot(ctx, application.InitiativeFilter{
		Limit: application.MaximumInitiativePage,
	}); err == nil {
		t.Fatal("InitiativeSnapshot(closed) error = nil")
	}
	if _, _, _, err := repository.InitiativeObservation(ctx, initiative.Handle); err == nil {
		t.Fatal("InitiativeObservation(closed) error = nil")
	}
	if _, _, _, err := repository.BacklogSnapshot(ctx, application.BacklogFilter{
		Limit: application.MaximumBacklogPage,
	}); err == nil {
		t.Fatal("BacklogSnapshot(closed) error = nil")
	}
}

func TestInitiativeSnapshotFiltersAndPaginatesBeforeMaterializing(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	expected := make([]string, 0, 17)
	for index := 0; index < 25; index++ {
		state := domain.InitiativeActive
		if index%7 == 0 {
			state = domain.InitiativeDelivered
		} else {
			expected = append(expected, fmt.Sprintf("initiative-page-%02d", index))
		}
		initiative := persistenceInitiative(fmt.Sprintf("initiative-page-%02d", index), state, int64(index+1))
		if err := store.CreateInitiative(ctx, initiative); err != nil {
			t.Fatalf("CreateInitiative(%q) error = %v", initiative.Handle, err)
		}
	}
	filter := application.InitiativeFilter{
		State: domain.InitiativeActive, Limit: application.MaximumInitiativePage,
	}
	first, cursor, _, err := store.InitiativeSnapshot(ctx, filter)
	if err != nil || len(first) != application.MaximumInitiativePage || cursor != expected[15] {
		t.Fatalf("InitiativeSnapshot(first) = %d initiatives, cursor %q, %v", len(first), cursor, err)
	}
	for index, initiative := range first {
		if initiative.Handle != expected[index] || initiative.State != filter.State {
			t.Fatalf("InitiativeSnapshot(first)[%d] = %#v", index, initiative)
		}
	}
	filter.AfterHandle = cursor
	second, cursor, _, err := store.InitiativeSnapshot(ctx, filter)
	if err != nil || len(second) != len(expected)-application.MaximumInitiativePage ||
		second[len(second)-1].Handle != expected[len(expected)-1] || cursor != "" {
		t.Fatalf("InitiativeSnapshot(second) = %#v, cursor %q, %v", second, cursor, err)
	}
	filter.AfterHandle = expected[len(expected)-1]
	empty, cursor, _, err := store.InitiativeSnapshot(ctx, filter)
	if err != nil || len(empty) != 0 || cursor != "" {
		t.Fatalf("InitiativeSnapshot(empty) = %#v, cursor %q, %v", empty, cursor, err)
	}
	filter.Limit = application.MaximumInitiativePage + 1
	if _, _, _, err := store.InitiativeSnapshot(ctx, filter); err == nil {
		t.Fatal("InitiativeSnapshot(oversized limit) error = nil")
	}
}

func TestBacklogSnapshotFiltersAndPaginatesBeforeMaterializing(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	expected := make([]string, 0, 17)
	for index := 0; index < 25; index++ {
		item := persistenceBacklogItem(fmt.Sprintf("backlog-page-%02d", index))
		switch index % 7 {
		case 0:
			item.RepositoryID = "repo-other"
		case 1:
			item.Readiness = domain.BacklogNeedsRefinement
		default:
			expected = append(expected, item.Handle)
		}
		if err := store.CreateBacklogItem(ctx, item); err != nil {
			t.Fatalf("CreateBacklogItem(%q) error = %v", item.Handle, err)
		}
	}
	filter := application.BacklogFilter{
		RepositoryID: "repo-primary", Readiness: domain.BacklogReady,
		Limit: application.MaximumBacklogPage,
	}
	first, cursor, _, err := store.BacklogSnapshot(ctx, filter)
	if err != nil || len(first) != application.MaximumBacklogPage || cursor != expected[15] {
		t.Fatalf("BacklogSnapshot(first) = %d items, cursor %q, %v", len(first), cursor, err)
	}
	for index, item := range first {
		if item.Handle != expected[index] || item.RepositoryID != filter.RepositoryID ||
			item.Readiness != filter.Readiness {
			t.Fatalf("BacklogSnapshot(first)[%d] = %#v", index, item)
		}
	}
	filter.AfterHandle = cursor
	second, cursor, _, err := store.BacklogSnapshot(ctx, filter)
	if err != nil || len(second) != 1 || second[0].Handle != expected[16] || cursor != "" {
		t.Fatalf("BacklogSnapshot(second) = %#v, cursor %q, %v", second, cursor, err)
	}
	filter.AfterHandle = expected[16]
	empty, cursor, _, err := store.BacklogSnapshot(ctx, filter)
	if err != nil || len(empty) != 0 || cursor != "" {
		t.Fatalf("BacklogSnapshot(empty) = %#v, cursor %q, %v", empty, cursor, err)
	}
	filter.Limit = application.MaximumBacklogPage + 1
	if _, _, _, err := store.BacklogSnapshot(ctx, filter); err == nil {
		t.Fatal("BacklogSnapshot(oversized limit) error = nil")
	}
}

func TestInitiativeAndBacklogWritesRejectInvalidAndDuplicateRecords(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := requireInitiativeBacklogRepository(t, store)
	initiative := persistenceInitiative("initiative-conflict-0001", domain.InitiativePreparing, 1)
	if err := repository.CreateInitiative(ctx, initiative); err != nil {
		t.Fatalf("CreateInitiative() error = %v", err)
	}
	if err := repository.CreateInitiative(ctx, initiative); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CreateInitiative(duplicate) error = %v, want ErrConflict", err)
	}
	invalidInitiative := persistenceInitiative("initiative-invalid-0001", domain.InitiativePreparing, 2)
	invalidInitiative.Edges = append(invalidInitiative.Edges, domain.InitiativeEdge{
		FromTaskHandle: "task-component-a", ToTaskHandle: "task-component-a", Kind: domain.EdgeBlocksStart,
	})
	if err := repository.CreateInitiative(ctx, invalidInitiative); err == nil {
		t.Fatal("CreateInitiative(cyclic) error = nil")
	}
	if _, err := repository.GetInitiative(ctx, invalidInitiative.Handle); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetInitiative(invalid) error = %v, want ErrNotFound", err)
	}

	backlog := persistenceBacklogItem("backlog-conflict-0001")
	if err := repository.CreateBacklogItem(ctx, backlog); err != nil {
		t.Fatalf("CreateBacklogItem() error = %v", err)
	}
	if err := repository.CreateBacklogItem(ctx, backlog); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("CreateBacklogItem(duplicate) error = %v, want ErrConflict", err)
	}
	invalidBacklog := persistenceBacklogItem("backlog-invalid-0001")
	invalidBacklog.DependsOn = []string{invalidBacklog.Handle}
	if err := repository.CreateBacklogItem(ctx, invalidBacklog); err == nil {
		t.Fatal("CreateBacklogItem(self dependency) error = nil")
	}
	if _, err := repository.GetBacklogItem(ctx, invalidBacklog.Handle); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("GetBacklogItem(invalid) error = %v, want ErrNotFound", err)
	}
}

func TestSeveralPreparingInitiativesMayAwaitDistinctHostGroupBindings(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := requireInitiativeBacklogRepository(t, store)
	first := persistenceInitiative("initiative-unbound-0001", domain.InitiativePreparing, 1)
	first.ManagedRunGroupID = ""
	second := persistenceInitiative("initiative-unbound-0002", domain.InitiativePreparing, 2)
	second.ManagedRunGroupID = ""
	if err := repository.CreateInitiative(ctx, first); err != nil {
		t.Fatalf("CreateInitiative(first unbound) error = %v", err)
	}
	if err := repository.CreateInitiative(ctx, second); err != nil {
		t.Fatalf("CreateInitiative(second unbound) error = %v", err)
	}
}

func TestStartupReconciliationPersistsUnknownForEveryAmbiguousInitiative(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	repository := requireInitiativeBacklogRepository(t, store)
	states := []domain.InitiativeState{
		domain.InitiativePreparing, domain.InitiativeActive, domain.InitiativeBlocked,
		domain.InitiativeIntegrating, domain.InitiativeValidating, domain.InitiativeCandidateComplete,
		domain.InitiativeDelivered, domain.InitiativeFailed, domain.InitiativeCancelled, domain.InitiativeUnknown,
	}
	for index, state := range states {
		initiative := persistenceInitiative(initiativeHandle(index+1), state, int64(index+10))
		if err := repository.CreateInitiative(ctx, initiative); err != nil {
			t.Fatalf("CreateInitiative(%q) error = %v", state, err)
		}
	}
	reconcileAt := time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC)
	result, err := store.ReconcileStartup(ctx, reconcileAt)
	if err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	if initiativeReconciliationCount(result) != 6 || result.StateVersion != 25 {
		t.Fatalf("ReconcileStartup() = %#v, want 6 initiatives and version 25", result)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatalf("Open(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := requireInitiativeBacklogRepository(t, reopened)
	initiatives, err := restarted.ListInitiatives(ctx)
	if err != nil {
		t.Fatalf("ListInitiatives() error = %v", err)
	}
	for index, initiative := range initiatives {
		want := states[index]
		if index < 6 {
			want = domain.InitiativeUnknown
			if !initiative.UpdatedAt.Equal(reconcileAt) {
				t.Fatalf("initiative %q updated at %s, want %s", initiative.Handle, initiative.UpdatedAt, reconcileAt)
			}
		}
		if initiative.State != want {
			t.Fatalf("initiative %q state = %q, want %q", initiative.Handle, initiative.State, want)
		}
	}
	second, err := reopened.ReconcileStartup(ctx, reconcileAt.Add(time.Hour))
	if err != nil || initiativeReconciliationCount(second) != 0 || second.StateVersion != 25 {
		t.Fatalf("ReconcileStartup(replay) = %#v, %v, want idempotent version 25", second, err)
	}
}

func TestStartupReconciliationRollsBackWhenStoredInitiativeIsCorrupt(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(canonicalTempDir(t), "devcrew.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	repository := requireInitiativeBacklogRepository(t, store)
	first := persistenceInitiative("initiative-rollback-0001", domain.InitiativeActive, 1)
	second := persistenceInitiative("initiative-rollback-0002", domain.InitiativeActive, 2)
	if err := repository.CreateInitiative(ctx, first); err != nil {
		t.Fatalf("CreateInitiative(first) error = %v", err)
	}
	if err := repository.CreateInitiative(ctx, second); err != nil {
		t.Fatalf("CreateInitiative(second) error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx,
		"UPDATE initiatives SET components_json = '{' WHERE handle = ?", second.Handle,
	); err != nil {
		t.Fatalf("corrupt stored initiative: %v", err)
	}
	if _, err := store.ReconcileStartup(ctx, first.UpdatedAt.Add(time.Hour)); err == nil {
		t.Fatal("ReconcileStartup(corrupt initiative) error = nil")
	}
	got, err := repository.GetInitiative(ctx, first.Handle)
	if err != nil {
		t.Fatalf("GetInitiative(first) error = %v", err)
	}
	if got.State != domain.InitiativeActive || got.StateVersion != first.StateVersion ||
		!got.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("first initiative changed despite rollback: %#v", got)
	}
}

func TestInitiativeMembershipMigrationBackfillsExistingGraphs(t *testing.T) {
	ctx := context.Background()
	databasePath := filepath.Join(canonicalTempDir(t), "devcrew.db")
	store, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	initiative := persistenceInitiative("initiative-membership-upgrade", domain.InitiativeActive, 1)
	if err := store.CreateInitiative(ctx, initiative); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(ctx, `DROP TABLE initiative_members;
		DELETE FROM schema_migrations WHERE version = 47`); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	for _, taskHandle := range initiativeTaskHandles(initiative) {
		got, found, err := initiativeForTask(ctx, reopened.db, taskHandle)
		if err != nil || !found || got.Handle != initiative.Handle {
			t.Fatalf("initiativeForTask(%q) = %#v, %t, %v", taskHandle, got, found, err)
		}
	}
}

func requireInitiativeBacklogRepository(t *testing.T, store *Store) initiativeBacklogRepository {
	t.Helper()
	repository, ok := any(store).(initiativeBacklogRepository)
	if !ok {
		t.Fatal("SQLite Store does not implement durable initiative and backlog repositories")
	}
	return repository
}

func initiativeReconciliationCount(result application.StartupReconciliation) int {
	field := reflect.ValueOf(result).FieldByName("InitiativesMarkedUnknown")
	if !field.IsValid() {
		return -1
	}
	return int(field.Int())
}

func persistenceInitiative(handle string, state domain.InitiativeState, version int64) domain.DevelopmentInitiative {
	now := time.Date(2026, time.August, 20, 12, 0, 0, 123456000, time.UTC)
	return domain.DevelopmentInitiative{
		SchemaVersion:     1,
		Handle:            handle,
		ManagedRunGroupID: "managed-run-group_" + handle,
		TitleRef:          "title-ref-0001",
		State:             state,
		BaseRevisionSet: []domain.InitiativeBaseRevision{
			{RepositoryID: "repo-primary", Revision: "0123456789abcdef0123456789abcdef01234567"},
		},
		Components: []domain.InitiativeComponent{
			{
				ComponentHandle: "component-a", RepositoryID: "repo-primary",
				ResponsibilityRef: "responsibility-ref-a", TaskHandles: []string{"task-component-a"},
			},
			{
				ComponentHandle: "component-integration", RepositoryID: "repo-primary",
				ResponsibilityRef: "responsibility-ref-integration", TaskHandles: []string{"task-integration"},
			},
		},
		Edges: []domain.InitiativeEdge{
			{FromTaskHandle: "task-component-a", ToTaskHandle: "task-integration", Kind: domain.EdgeIntegratesAfter},
		},
		ContractArtifacts:    []string{"contract-artifact-0001"},
		IntegrationPolicyID:  "integration-default",
		IntegrationOwnerTask: "task-integration",
		StateVersion:         version,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func persistenceBacklogItem(handle string) domain.BacklogItem {
	now := time.Date(2026, time.August, 20, 13, 0, 0, 654321000, time.UTC)
	return domain.BacklogItem{
		SchemaVersion:         1,
		Handle:                handle,
		RepositoryID:          "repo-primary",
		Shape:                 domain.ShapeShip,
		RequestedOutcome:      "Persist the exact bounded request across a service restart.",
		DependsOn:             []string{"backlog-dependency-0001"},
		Priority:              domain.BacklogPriorityHigh,
		Readiness:             domain.BacklogReady,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func initiativeHandle(index int) string {
	return fmt.Sprintf("initiative-reconcile-%04d", index)
}
