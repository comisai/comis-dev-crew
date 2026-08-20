package localapi

import (
	"context"
	"reflect"
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
		Queries: &apiQueries{}, InitiativeQueries: reads, Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	socketPath := startHandlerServer(t, handler, CallerMCPFacade)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}

	list, err := client.ListInitiatives(context.Background(), "read-initiative-list", ListInitiativesInput{
		State: domain.InitiativeActive,
	})
	if err != nil || !reflect.DeepEqual(list, reads.list) || reads.state != domain.InitiativeActive {
		t.Fatalf("ListInitiatives() = %#v, %v; state = %q", list, err, reads.state)
	}
	detail, err := client.GetInitiative(context.Background(), "read-initiative-detail", "initiative-query")
	if err != nil || !reflect.DeepEqual(detail, reads.detail) || reads.handle != "initiative-query" {
		t.Fatalf("GetInitiative() = %#v, %v; handle = %q", detail, err, reads.handle)
	}
	filter := application.BacklogFilter{RepositoryID: "repo-primary", Readiness: domain.BacklogReady}
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
	list    application.InitiativeList
	detail  application.InitiativeDetail
	backlog application.BacklogList
	state   domain.InitiativeState
	handle  string
	filter  application.BacklogFilter
}

func (queries *apiInitiativeQueries) ListInitiatives(
	_ context.Context,
	state domain.InitiativeState,
) (application.InitiativeList, error) {
	queries.state = state
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
