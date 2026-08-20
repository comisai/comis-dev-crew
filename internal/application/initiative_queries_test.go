package application

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestInitiativeQueriesProjectFilteredListsAndDetailedGraph(t *testing.T) {
	observedAt := time.Date(2026, time.August, 20, 15, 0, 0, 0, time.UTC)
	active := graphInitiative()
	active.StateVersion = 11
	delivered := active
	delivered.Handle = "initiative-delivered"
	delivered.State = domain.InitiativeDelivered
	delivered.StateVersion = 9
	store := &initiativeQueryStoreFixture{
		initiatives: []domain.DevelopmentInitiative{delivered, active},
		initiative:  active,
		tasks: []domain.Task{
			{Handle: "task-backend", State: domain.TaskWorking},
			{Handle: "task-frontend", State: domain.TaskReady},
			{Handle: "task-integration", State: domain.TaskPrepared},
		},
		stateVersion: 11,
	}
	queries, err := NewInitiativeQueries(InitiativeQueryConfig{
		Store: store, Clock: func() time.Time { return observedAt },
	})
	if err != nil {
		t.Fatalf("NewInitiativeQueries() error = %v", err)
	}

	list, err := queries.ListInitiatives(context.Background(), domain.InitiativeActive)
	if err != nil {
		t.Fatalf("ListInitiatives() error = %v", err)
	}
	if list.SchemaVersion != 1 || list.StateVersion != 11 || list.CapturedAtMs != observedAt.UnixMilli() ||
		len(list.Initiatives) != 1 || list.Initiatives[0].InitiativeHandle != active.Handle ||
		list.Initiatives[0].TaskCount != 3 || list.Initiatives[0].ComponentCount != 3 {
		t.Fatalf("ListInitiatives() = %#v", list)
	}

	detail, err := queries.GetInitiative(context.Background(), active.Handle)
	if err != nil {
		t.Fatalf("GetInitiative() error = %v", err)
	}
	if detail.StateVersion != 11 || detail.Initiative.Handle != active.Handle ||
		detail.Graph.Completeness != CompletenessComplete || len(detail.Graph.Nodes) != 3 ||
		detail.ReasonCode != "initiative_active" ||
		!reflect.DeepEqual(detail.NextSafeActions, []InitiativeNextAction{InitiativeActionPause, InitiativeActionCancel}) {
		t.Fatalf("GetInitiative() = %#v", detail)
	}
	if !detail.Graph.ObservedAt.Equal(observedAt) {
		t.Fatalf("graph observation time = %v, want %v", detail.Graph.ObservedAt, observedAt)
	}
}

func TestInitiativeQueriesScopeBacklogWithoutInventingRunAuthority(t *testing.T) {
	observedAt := time.Date(2026, time.August, 20, 16, 0, 0, 0, time.UTC)
	ready := queryBacklogItem("backlog-ready", "repo-primary", domain.BacklogReady)
	otherRepository := queryBacklogItem("backlog-other", "repo-other", domain.BacklogReady)
	needsRefinement := queryBacklogItem("backlog-refine", "repo-primary", domain.BacklogNeedsRefinement)
	store := &initiativeQueryStoreFixture{
		backlog: []domain.BacklogItem{otherRepository, needsRefinement, ready}, stateVersion: 14,
	}
	queries, err := NewInitiativeQueries(InitiativeQueryConfig{
		Store: store, Clock: func() time.Time { return observedAt },
	})
	if err != nil {
		t.Fatalf("NewInitiativeQueries() error = %v", err)
	}

	list, err := queries.ListBacklog(context.Background(), BacklogFilter{
		RepositoryID: "repo-primary", Readiness: domain.BacklogReady,
	})
	if err != nil {
		t.Fatalf("ListBacklog() error = %v", err)
	}
	if list.SchemaVersion != 1 || list.StateVersion != 14 || list.CapturedAtMs != observedAt.UnixMilli() ||
		len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0], ready) {
		t.Fatalf("ListBacklog() = %#v", list)
	}
	for _, field := range domain.BacklogItemFieldNames(list.Items[0]) {
		switch field {
		case "ManagedRunID", "WorkspaceLeaseID", "ExecutionAttachmentID", "Credential", "DeliveryMode":
			t.Fatalf("backlog projection gained run authority field %q", field)
		}
	}
}

func TestInitiativeQueriesRejectInvalidScopesAndTranslateStoreFailures(t *testing.T) {
	store := &initiativeQueryStoreFixture{err: applicationQueryTestError("store unavailable")}
	queries, err := NewInitiativeQueries(InitiativeQueryConfig{Store: store, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewInitiativeQueries() error = %v", err)
	}
	assertFailureCode(t, func() error {
		_, err := queries.ListInitiatives(context.Background(), domain.InitiativeState("invented"))
		return err
	}(), domain.ErrorInvalidArgument)
	assertFailureCode(t, func() error {
		_, err := queries.ListBacklog(context.Background(), BacklogFilter{Readiness: domain.BacklogReadiness("invented")})
		return err
	}(), domain.ErrorInvalidArgument)
	assertFailureCode(t, func() error {
		_, err := queries.ListBacklog(context.Background(), BacklogFilter{RepositoryID: "bad repository"})
		return err
	}(), domain.ErrorInvalidArgument)
	if store.snapshotCalls != 0 {
		t.Fatalf("invalid scopes reached the store %d times", store.snapshotCalls)
	}
	assertFailureCode(t, func() error {
		_, err := queries.GetInitiative(context.Background(), "initiative-valid")
		return err
	}(), domain.ErrorInternal)
	assertFailureCode(t, func() error {
		store.err = ErrNotFound
		_, err := queries.GetInitiative(context.Background(), "initiative-missing")
		return err
	}(), domain.ErrorNotFound)
	if _, err := NewInitiativeQueries(InitiativeQueryConfig{}); err == nil {
		t.Fatal("NewInitiativeQueries(empty) error = nil")
	}
}

type initiativeQueryStoreFixture struct {
	initiatives   []domain.DevelopmentInitiative
	initiative    domain.DevelopmentInitiative
	tasks         []domain.Task
	backlog       []domain.BacklogItem
	stateVersion  int64
	err           error
	snapshotCalls int
}

func (store *initiativeQueryStoreFixture) InitiativeSnapshot(
	context.Context,
) ([]domain.DevelopmentInitiative, int64, error) {
	store.snapshotCalls++
	return append([]domain.DevelopmentInitiative(nil), store.initiatives...), store.stateVersion, store.err
}

func (store *initiativeQueryStoreFixture) InitiativeObservation(
	context.Context,
	string,
) (domain.DevelopmentInitiative, []domain.Task, int64, error) {
	store.snapshotCalls++
	return store.initiative, append([]domain.Task(nil), store.tasks...), store.stateVersion, store.err
}

func (store *initiativeQueryStoreFixture) BacklogSnapshot(
	context.Context,
) ([]domain.BacklogItem, int64, error) {
	store.snapshotCalls++
	return append([]domain.BacklogItem(nil), store.backlog...), store.stateVersion, store.err
}

type applicationQueryTestError string

func (failure applicationQueryTestError) Error() string { return string(failure) }

func queryBacklogItem(handle, repositoryID string, readiness domain.BacklogReadiness) domain.BacklogItem {
	now := time.Date(2026, time.August, 20, 13, 0, 0, 0, time.UTC)
	return domain.BacklogItem{
		SchemaVersion: 1, Handle: handle, RepositoryID: repositoryID, Shape: domain.ShapeShip,
		RequestedOutcome: "Implement the bounded request.", DependsOn: []string{},
		Priority: domain.BacklogPriorityNormal, Readiness: readiness,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
		CreatedAt:             now, UpdatedAt: now,
	}
}

func assertFailureCode(t *testing.T, err error, want domain.ErrorCode) {
	t.Helper()
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != want {
		t.Fatalf("error = %v, want failure code %q", err, want)
	}
}
