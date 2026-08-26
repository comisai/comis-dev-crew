package localapi

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestServerClientBacklogMutationsPreserveBoundedAuthority(t *testing.T) {
	now := time.Now().UTC()
	addition := backlogAdditionAPIFixture(now)
	promotion := backlogPromotionAPIFixture(now)
	mutations := &apiBacklogMutations{addition: addition, promotion: promotion}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, BacklogAdditions: mutations, BacklogPromotions: mutations,
		ServiceInstanceID: "service-instance_a", Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(startHandlerServer(t, handler, CallerMCPFacade), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	addInput := AddBacklogInput{
		RepositoryID: "repo-primary", Shape: domain.ShapeShip,
		RequestedOutcome: "Implement the bounded request.", DependsOn: []string{"backlog-dependency"},
		Priority: domain.BacklogPriorityNormal, Readiness: domain.BacklogReady,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG",
	}
	added, err := client.AddBacklog(context.Background(), "operation-add-backlog-api", addInput)
	if err != nil {
		t.Fatalf("AddBacklog() error = %v", err)
	}
	if !reflect.DeepEqual(added.Item, addition.Item) || added.OperationID != "operation-add-backlog-api" ||
		added.StateVersion != 14 || added.SideEffect != SideEffectMutate {
		t.Fatalf("AddBacklog() = %#v", added)
	}
	wantAddition := application.BacklogAdditionCommand{
		OperationID: "operation-add-backlog-api", RepositoryID: addInput.RepositoryID, Shape: addInput.Shape,
		RequestedOutcome: addInput.RequestedOutcome, DependsOn: addInput.DependsOn,
		Priority: addInput.Priority, Readiness: addInput.Readiness,
		SourceConversationRef: addInput.SourceConversationRef,
	}
	if !reflect.DeepEqual(mutations.addCommand, wantAddition) {
		t.Fatalf("canonical addition command = %#v, want %#v", mutations.addCommand, wantAddition)
	}
	promoteInput := PromoteBacklogInput{
		BacklogHandle: "backlog-added", BaseRevision: strings.Repeat("a", 40),
		AcceptanceCriteria: []string{"The implementation is verified."}, Constraints: []string{"Preserve the API."},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "codex-reviewed",
	}
	promoted, err := client.PromoteBacklog(context.Background(), "operation-promote-backlog-api", promoteInput)
	if err != nil {
		t.Fatalf("PromoteBacklog() error = %v", err)
	}
	if promoted.BacklogHandle != promotion.Item.Handle || promoted.TaskHandle != promotion.Task.Handle ||
		promoted.State != domain.TaskPrepared || promoted.StateVersion != 16 || promoted.TaskStateVersion != 15 ||
		promoted.SideEffect != SideEffectMutate || !reflect.DeepEqual(promoted.ManagedRun, *promotion.Preparation) {
		t.Fatalf("PromoteBacklog() = %#v", promoted)
	}
	wantPromotion := application.BacklogPromotionCommand{
		OperationID: "operation-promote-backlog-api", ServiceInstanceID: "service-instance_a",
		BacklogHandle: promoteInput.BacklogHandle, BaseRevision: promoteInput.BaseRevision,
		AcceptanceCriteria: promoteInput.AcceptanceCriteria, Constraints: promoteInput.Constraints,
		ValidationProfile: promoteInput.ValidationProfile, DeliveryMode: promoteInput.DeliveryMode,
		WorkerProfileID: promoteInput.WorkerProfileID,
	}
	if !reflect.DeepEqual(mutations.promoteCommand, wantPromotion) {
		t.Fatalf("canonical promotion command = %#v, want %#v", mutations.promoteCommand, wantPromotion)
	}
	if !MethodAddBacklog.valid() || !MethodPromoteBacklog.valid() ||
		MethodAddBacklog.SideEffect() != SideEffectMutate || MethodPromoteBacklog.SideEffect() != SideEffectMutate {
		t.Fatalf("backlog method posture = %v/%v", MethodAddBacklog.SideEffect(), MethodPromoteBacklog.SideEffect())
	}
}

func TestBacklogMutationBoundaryRejectsForgedAuthorityAndIncompleteResults(t *testing.T) {
	now := time.Now().UTC()
	mutations := &apiBacklogMutations{
		addition: backlogAdditionAPIFixture(now), promotion: backlogPromotionAPIFixture(now),
	}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, BacklogAdditions: mutations, BacklogPromotions: mutations,
		ServiceInstanceID: "service-instance_a", Clock: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{
		`{"protocolVersion":"devcrew.local.v1","operationId":"operation-add-forged","method":"AddBacklog","payload":{"repositoryId":"repo-primary","shape":"ship","requestedOutcome":"Implement it.","dependsOn":[],"priority":"normal","readiness":"ready","sourceConversationRef":"cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG","workspaceRoot":"/forged"}}`,
		`{"protocolVersion":"devcrew.local.v1","operationId":"operation-promote-forged","method":"PromoteBacklog","payload":{"backlogHandle":"backlog-added","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Verify it."],"constraints":[],"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"codex-reviewed","taskHandle":"task-forged"}}`,
	} {
		outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(request))
		if outcome.Error == nil || outcome.Error.Code != domain.ErrorInvalidArgument {
			t.Fatalf("forged backlog outcome = %#v", outcome)
		}
	}
	if mutations.addCalls != 0 || mutations.promoteCalls != 0 {
		t.Fatalf("forged calls reached mutations = %d/%d", mutations.addCalls, mutations.promoteCalls)
	}

	mutations.addition.Operation.ResultRef = "backlog-substituted"
	addRequest := `{"protocolVersion":"devcrew.local.v1","operationId":"operation-add-backlog-api","method":"AddBacklog","payload":{"repositoryId":"repo-primary","shape":"ship","requestedOutcome":"Implement it.","dependsOn":[],"priority":"normal","readiness":"ready","sourceConversationRef":"cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"}}`
	if outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(addRequest)); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("incomplete addition outcome = %#v", outcome)
	}
	mutations.promotion.Preparation = nil
	promoteRequest := `{"protocolVersion":"devcrew.local.v1","operationId":"operation-promote-backlog-api","method":"PromoteBacklog","payload":{"backlogHandle":"backlog-added","baseRevision":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","acceptanceCriteria":["Verify it."],"constraints":[],"validationProfile":"go-default","deliveryMode":"pull_request","workerProfileId":"codex-reviewed"}}`
	if outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(promoteRequest)); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("incomplete promotion outcome = %#v", outcome)
	}
	mutations.addErr = errors.New("addition dependency failed")
	if outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(addRequest)); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("failed addition outcome = %#v", outcome)
	}
	mutations.promoteErr = errors.New("promotion dependency failed")
	if outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(promoteRequest)); outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorInternal {
		t.Fatalf("failed promotion outcome = %#v", outcome)
	}
	readOnly, err := NewHandler(HandlerConfig{Queries: &apiQueries{}, Clock: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []string{addRequest, promoteRequest} {
		outcome := readOnly.handle(context.Background(), CallerMCPFacade, []byte(request))
		if outcome.Error == nil || outcome.Error.Code != domain.ErrorUnavailable {
			t.Fatalf("unconfigured backlog outcome = %#v", outcome)
		}
	}
}

type apiBacklogMutations struct {
	addition       application.BacklogAdditionResult
	promotion      application.BacklogPromotionResult
	addCommand     application.BacklogAdditionCommand
	promoteCommand application.BacklogPromotionCommand
	addCalls       int
	promoteCalls   int
	addErr         error
	promoteErr     error
}

func (mutations *apiBacklogMutations) AddBacklog(
	_ context.Context,
	command application.BacklogAdditionCommand,
) (application.BacklogAdditionResult, error) {
	mutations.addCalls++
	mutations.addCommand = command
	return mutations.addition, mutations.addErr
}

func (mutations *apiBacklogMutations) PromoteBacklog(
	_ context.Context,
	command application.BacklogPromotionCommand,
) (application.BacklogPromotionResult, error) {
	mutations.promoteCalls++
	mutations.promoteCommand = command
	return mutations.promotion, mutations.promoteErr
}

func backlogAdditionAPIFixture(now time.Time) application.BacklogAdditionResult {
	item := domain.BacklogItem{
		SchemaVersion: 1, Handle: "backlog-added", RepositoryID: "repo-primary", Shape: domain.ShapeShip,
		RequestedOutcome: "Implement the bounded request.", DependsOn: []string{"backlog-dependency"},
		Priority: domain.BacklogPriorityNormal, Readiness: domain.BacklogReady,
		SourceConversationRef: "cv_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", CreatedAt: now, UpdatedAt: now,
	}
	return application.BacklogAdditionResult{Item: item, Operation: domain.OperationRecord{
		SchemaVersion: 1, ID: "operation-add-backlog-api", Command: "AddBacklog",
		SubjectDigest: strings.Repeat("a", 64), Status: domain.OperationCompleted,
		ResultRef: item.Handle, StateVersion: 14, CreatedAt: now, UpdatedAt: now,
	}}
}

func backlogPromotionAPIFixture(now time.Time) application.BacklogPromotionResult {
	addition := backlogAdditionAPIFixture(now)
	addition.Item.Readiness = domain.BacklogPromoted
	task := domain.Task{
		SchemaVersion: 1, Handle: "task-backlog-promoted", ServiceInstanceID: "service-instance_a",
		State: domain.TaskPrepared, Shape: addition.Item.Shape, RepositoryID: addition.Item.RepositoryID,
		BaseRevision: strings.Repeat("a", 40), BriefRevision: 1,
		AcceptanceCriteria: []string{addition.Item.RequestedOutcome, "The implementation is verified."},
		Constraints:        []string{"Preserve the API."}, ValidationProfile: "go-default",
		DeliveryMode: domain.DeliveryPullRequest, WorkerProfileID: "codex-reviewed",
		StateVersion: 15, CreatedAt: now, UpdatedAt: now,
	}
	preparation := initiativeMemberPreparation(now, task.Handle, "registration-nonce_backlog")
	return application.BacklogPromotionResult{
		Item: addition.Item, Task: task, Preparation: &preparation,
		Operation: domain.OperationRecord{
			SchemaVersion: 1, ID: "operation-promote-backlog-api", Command: "PromoteBacklog",
			SubjectDigest: strings.Repeat("b", 64), Status: domain.OperationCompleted,
			ResultRef: task.Handle, StateVersion: 16, CreatedAt: now, UpdatedAt: now,
		},
	}
}
