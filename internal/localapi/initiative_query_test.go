package localapi

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestServerClient_InitiativeAndBacklogReadsUseCanonicalProjections(t *testing.T) {
	now := time.Date(2026, time.August, 20, 17, 0, 0, 0, time.UTC)
	reads := &apiInitiativeQueries{
		list: application.InitiativeList{
			SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 21,
			Initiatives: []application.InitiativeSummary{{
				InitiativeHandle: "initiative-query", State: domain.InitiativeActive, StateVersion: 21,
			}},
		},
		detail: application.InitiativeDetail{
			SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 21,
			Initiative: domain.DevelopmentInitiative{Handle: "initiative-query", State: domain.InitiativeActive},
			Graph: application.InitiativeGraphView{
				InitiativeHandle: "initiative-query", State: domain.InitiativeActive, StateVersion: 21,
				Nodes: []application.InitiativeGraphNode{}, Edges: []application.InitiativeGraphEdge{},
			},
			ReasonCode: "initiative_active",
		},
		backlog: application.BacklogList{
			SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 21,
			Items: []domain.BacklogItem{{Handle: "backlog-query", Readiness: domain.BacklogReady}},
		},
	}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeQueries: reads, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	initiativeFilter := application.InitiativeFilter{
		State: domain.InitiativeActive, AfterHandle: "initiative-before", Limit: 7,
	}
	list, err := client.ListInitiatives(context.Background(), "read-initiative-list", ListInitiativesInput{
		State: initiativeFilter.State, AfterHandle: initiativeFilter.AfterHandle, Limit: initiativeFilter.Limit,
	})
	if err != nil || !reflect.DeepEqual(list, reads.list) || reads.initiativeFilter != initiativeFilter {
		t.Fatalf("ListInitiatives() = %#v, %v; filter = %#v", list, err, reads.initiativeFilter)
	}
	detail, err := client.GetInitiative(context.Background(), "read-initiative-detail", "initiative-query")
	if err != nil || !reflect.DeepEqual(detail, reads.detail) || reads.handle != "initiative-query" {
		t.Fatalf("GetInitiative() = %#v, %v; handle = %q", detail, err, reads.handle)
	}
	filter := application.BacklogFilter{
		RepositoryID: "repo-primary", Readiness: domain.BacklogReady,
		AfterHandle: "backlog-before", Limit: 7,
	}
	backlog, err := client.ListBacklog(context.Background(), "read-backlog-list", ListBacklogInput(filter))
	if err != nil || !reflect.DeepEqual(backlog, reads.backlog) || !reflect.DeepEqual(reads.filter, filter) {
		t.Fatalf("ListBacklog() = %#v, %v; filter = %#v", backlog, err, reads.filter)
	}

	for _, method := range []Method{MethodListInitiatives, MethodGetInitiative, MethodListBacklog} {
		if !method.valid() || method.SideEffect() != SideEffectRead {
			t.Fatalf("method %q posture = %v/%q", method, method.valid(), method.SideEffect())
		}
	}
}

func TestServerClient_InitiativeAndBacklogPagesStayWithinResponseLimit(t *testing.T) {
	now := time.Date(2026, time.August, 20, 18, 0, 0, 0, time.UTC)
	dependencies := make([]string, 64)
	for index := range dependencies {
		dependencies[index] = fmt.Sprintf("dependency-%02d-%s", index, strings.Repeat("a", 49))
	}
	items := make([]domain.BacklogItem, application.MaximumBacklogPage)
	for index := range items {
		items[index] = domain.BacklogItem{
			SchemaVersion:         1,
			Handle:                fmt.Sprintf("backlog-page-%02d-%s", index, strings.Repeat("a", 47)),
			RepositoryID:          "repository-" + strings.Repeat("a", 52),
			Shape:                 domain.ShapeShip,
			RequestedOutcome:      strings.Repeat("\x01", 8192),
			DependsOn:             append([]string(nil), dependencies...),
			Priority:              domain.BacklogPriorityHigh,
			Readiness:             domain.BacklogReady,
			SourceConversationRef: "c" + strings.Repeat("a", 255),
			CreatedAt:             now,
			UpdatedAt:             now,
		}
		if err := items[index].Validate(); err != nil {
			t.Fatalf("BacklogItem[%d].Validate() error = %v", index, err)
		}
	}
	reads := &apiInitiativeQueries{backlog: application.BacklogList{
		SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 22,
		NextCursor: items[len(items)-1].Handle, Items: items,
	}}
	initiatives := make([]application.InitiativeSummary, application.MaximumInitiativePage)
	for index := range initiatives {
		initiatives[index] = application.InitiativeSummary{
			InitiativeHandle: fmt.Sprintf("initiative-page-%02d", index), TitleRef: strings.Repeat("t", 256),
			State: domain.InitiativeActive, StateVersion: int64(index + 1), UpdatedAt: now,
		}
	}
	reads.list = application.InitiativeList{
		SchemaVersion: 1, CapturedAtMs: now.UnixMilli(), StateVersion: 22,
		NextCursor: initiatives[len(initiatives)-1].InitiativeHandle, Initiatives: initiatives,
	}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, InitiativeQueries: reads, Clock: time.Now,
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	client, err := NewClient(startHandlerServer(t, handler, CallerMCPFacade), time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	page, err := client.ListBacklog(context.Background(), "read-backlog-page", ListBacklogInput{
		Limit: application.MaximumBacklogPage,
	})
	if err != nil || len(page.Items) != application.MaximumBacklogPage ||
		page.NextCursor != items[len(items)-1].Handle {
		t.Fatalf("ListBacklog(maximum page) = %d items, cursor %q, %v", len(page.Items), page.NextCursor, err)
	}
	initiativePage, err := client.ListInitiatives(context.Background(), "read-initiative-page", ListInitiativesInput{
		Limit: application.MaximumInitiativePage,
	})
	if err != nil || len(initiativePage.Initiatives) != application.MaximumInitiativePage ||
		initiativePage.NextCursor != initiatives[len(initiatives)-1].InitiativeHandle {
		t.Fatalf("ListInitiatives(maximum page) = %d initiatives, cursor %q, %v",
			len(initiativePage.Initiatives), initiativePage.NextCursor, err)
	}
}

func TestInitiativeReadBoundaryRefusesBroadenedAndUnavailableRequests(t *testing.T) {
	handler, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	requests := []string{
		`{"protocolVersion":"devcrew.local.v1","operationId":"read-initiative-list","method":"ListInitiatives","payload":{"state":"active","scope":"all"}}`,
		`{"protocolVersion":"devcrew.local.v1","operationId":"read-initiative-detail","method":"GetInitiative","payload":{"initiativeHandle":"initiative-query","taskHandle":"task-other"}}`,
		`{"protocolVersion":"devcrew.local.v1","operationId":"read-backlog-list","method":"ListBacklog","payload":{"repositoryId":"repo-primary","readiness":"ready","managedRunId":"forged"}}`,
	}
	for _, request := range requests {
		outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(request))
		if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
			outcome.Error.Code != domain.ErrorInvalidArgument {
			t.Fatalf("broadened read outcome = %#v", outcome)
		}
	}
	valid := []string{
		`{"protocolVersion":"devcrew.local.v1","operationId":"read-initiative-list","method":"ListInitiatives","payload":{}}`,
		`{"protocolVersion":"devcrew.local.v1","operationId":"read-initiative-detail","method":"GetInitiative","payload":{"initiativeHandle":"initiative-query"}}`,
		`{"protocolVersion":"devcrew.local.v1","operationId":"read-backlog-list","method":"ListBacklog","payload":{}}`,
	}
	for _, request := range valid {
		outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(request))
		if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
			outcome.Error.Code != domain.ErrorUnavailable {
			t.Fatalf("unavailable read outcome = %#v", outcome)
		}
	}
}

type apiInitiativeQueries struct {
	list             application.InitiativeList
	detail           application.InitiativeDetail
	backlog          application.BacklogList
	initiativeFilter application.InitiativeFilter
	handle           string
	filter           application.BacklogFilter
}

func (queries *apiInitiativeQueries) ListInitiatives(
	_ context.Context,
	filter application.InitiativeFilter,
) (application.InitiativeList, error) {
	queries.initiativeFilter = filter
	return queries.list, nil
}

func (queries *apiInitiativeQueries) GetInitiative(
	_ context.Context,
	handle string,
) (application.InitiativeDetail, error) {
	queries.handle = handle
	return queries.detail, nil
}

func (queries *apiInitiativeQueries) ListBacklog(
	_ context.Context,
	filter application.BacklogFilter,
) (application.BacklogList, error) {
	queries.filter = filter
	return queries.backlog, nil
}
