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
	store := &initiativeQueryStoreFixture{
		initiatives: []domain.DevelopmentInitiative{active}, nextCursor: active.Handle,
		initiative: active,
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

	list, err := queries.ListInitiatives(context.Background(), InitiativeFilter{
		State: domain.InitiativeActive, AfterHandle: "initiative-before",
	})
	if err != nil {
		t.Fatalf("ListInitiatives() error = %v", err)
	}
	if list.SchemaVersion != 1 || list.StateVersion != 11 || list.CapturedAtMs != observedAt.UnixMilli() ||
		list.NextCursor != active.Handle || len(list.Initiatives) != 1 || list.Initiatives[0].InitiativeHandle != active.Handle ||
		list.Initiatives[0].TaskCount != 3 || list.Initiatives[0].ComponentCount != 3 {
		t.Fatalf("ListInitiatives() = %#v", list)
	}
	if store.initiativeFilter != (InitiativeFilter{
		State: domain.InitiativeActive, AfterHandle: "initiative-before", Limit: MaximumInitiativePage,
	}) {
		t.Fatalf("InitiativeSnapshot() filter = %#v", store.initiativeFilter)
	}
	if _, err := queries.ListInitiatives(context.Background(), InitiativeFilter{
		State: domain.InitiativeActive, Limit: MaximumInitiativePage + 10,
	}); err != nil || store.initiativeFilter.Limit != MaximumInitiativePage {
		t.Fatalf("ListInitiatives(oversized limit) filter = %#v, %v", store.initiativeFilter, err)
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
	store := &initiativeQueryStoreFixture{
		backlog: []domain.BacklogItem{ready}, nextCursor: ready.Handle, stateVersion: 14,
	}
	queries, err := NewInitiativeQueries(InitiativeQueryConfig{
		Store: store, Clock: func() time.Time { return observedAt },
	})
	if err != nil {
		t.Fatalf("NewInitiativeQueries() error = %v", err)
	}

	list, err := queries.ListBacklog(context.Background(), BacklogFilter{
		RepositoryID: "repo-primary", Readiness: domain.BacklogReady, AfterHandle: "backlog-before",
	})
	if err != nil {
		t.Fatalf("ListBacklog() error = %v", err)
	}
	if list.SchemaVersion != 1 || list.StateVersion != 14 || list.CapturedAtMs != observedAt.UnixMilli() ||
		list.NextCursor != ready.Handle || len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0], ready) {
		t.Fatalf("ListBacklog() = %#v", list)
	}
	if store.backlogFilter != (BacklogFilter{
		RepositoryID: "repo-primary", Readiness: domain.BacklogReady,
		AfterHandle: "backlog-before", Limit: MaximumBacklogPage,
	}) {
		t.Fatalf("BacklogSnapshot() filter = %#v", store.backlogFilter)
	}
	if _, err := queries.ListBacklog(context.Background(), BacklogFilter{
		RepositoryID: "repo-primary", Limit: MaximumBacklogPage + 10,
	}); err != nil || store.backlogFilter.Limit != MaximumBacklogPage {
		t.Fatalf("ListBacklog(oversized limit) filter = %#v, %v", store.backlogFilter, err)
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
		_, err := queries.ListInitiatives(context.Background(), InitiativeFilter{State: domain.InitiativeState("invented")})
		return err
	}(), domain.ErrorInvalidArgument)
	assertFailureCode(t, func() error {
		_, err := queries.ListInitiatives(context.Background(), InitiativeFilter{AfterHandle: "bad cursor"})
		return err
	}(), domain.ErrorInvalidArgument)
	assertFailureCode(t, func() error {
		_, err := queries.ListInitiatives(context.Background(), InitiativeFilter{Limit: -1})
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
	assertFailureCode(t, func() error {
		_, err := queries.ListBacklog(context.Background(), BacklogFilter{AfterHandle: "bad cursor"})
		return err
	}(), domain.ErrorInvalidArgument)
	assertFailureCode(t, func() error {
		_, err := queries.ListBacklog(context.Background(), BacklogFilter{Limit: -1})
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

func TestInitiativeStateExplanationCoversEveryClosedPosture(t *testing.T) {
	tests := []struct {
		state      domain.InitiativeState
		wantReason string
		wantAction InitiativeNextAction
	}{
		{state: domain.InitiativePreparing, wantReason: "initiative_preparing", wantAction: InitiativeActionInspect},
		{state: domain.InitiativeIntegrating, wantReason: "initiative_integrating", wantAction: InitiativeActionPause},
		{state: domain.InitiativeValidating, wantReason: "initiative_validating", wantAction: InitiativeActionPause},
		{state: domain.InitiativeBlocked, wantReason: "initiative_blocked", wantAction: InitiativeActionInspect},
		{state: domain.InitiativeUnknown, wantReason: "initiative_unknown", wantAction: InitiativeActionInspect},
		{state: domain.InitiativeCandidateComplete, wantReason: "initiative_candidate_complete", wantAction: InitiativeActionInspect},
		{state: domain.InitiativeDelivered, wantReason: "initiative_delivered", wantAction: InitiativeActionNone},
		{state: domain.InitiativeFailed, wantReason: "initiative_failed", wantAction: InitiativeActionNone},
		{state: domain.InitiativeCancelled, wantReason: "initiative_cancelled", wantAction: InitiativeActionNone},
		{state: domain.InitiativeState("invented"), wantReason: "initiative_unknown", wantAction: InitiativeActionInspect},
	}
	for _, test := range tests {
		reason, explanation, actions := explainInitiativeState(test.state)
		if reason != test.wantReason || explanation == "" || len(actions) == 0 || actions[0] != test.wantAction {
			t.Fatalf("explainInitiativeState(%q) = %q/%q/%#v", test.state, reason, explanation, actions)
		}
	}
}

type initiativeQueryStoreFixture struct {
	initiatives      []domain.DevelopmentInitiative
	initiative       domain.DevelopmentInitiative
	tasks            []domain.Task
	backlog          []domain.BacklogItem
	nextCursor       string
	initiativeFilter InitiativeFilter
	backlogFilter    BacklogFilter
	stateVersion     int64
	err              error
	snapshotCalls    int
}

func (store *initiativeQueryStoreFixture) InitiativeSnapshot(
	_ context.Context,
	filter InitiativeFilter,
) ([]domain.DevelopmentInitiative, string, int64, error) {
	store.snapshotCalls++
	store.initiativeFilter = filter
	return append([]domain.DevelopmentInitiative(nil), store.initiatives...), store.nextCursor, store.stateVersion, store.err
}

func (store *initiativeQueryStoreFixture) InitiativeObservation(
	context.Context,
	string,
) (domain.DevelopmentInitiative, []domain.Task, int64, error) {
	store.snapshotCalls++
	return store.initiative, append([]domain.Task(nil), store.tasks...), store.stateVersion, store.err
}

func (store *initiativeQueryStoreFixture) BacklogSnapshot(
	_ context.Context,
	filter BacklogFilter,
) ([]domain.BacklogItem, string, int64, error) {
	store.snapshotCalls++
	store.backlogFilter = filter
	return append([]domain.BacklogItem(nil), store.backlog...), store.nextCursor, store.stateVersion, store.err
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
