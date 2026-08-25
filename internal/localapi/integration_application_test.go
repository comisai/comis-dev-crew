package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestIntegrationApplicationMethodIsAReachableMutationSurface(t *testing.T) {
	method := Method("ApplyIntegrationCandidate")
	if !method.valid() || method.SideEffect() != SideEffectMutate || !methodAllowed(CallerMCPFacade, method) {
		t.Fatalf("integration method posture = valid:%t sideEffect:%q mcp:%t",
			method.valid(), method.SideEffect(), methodAllowed(CallerMCPFacade, method))
	}
	handler := newTestHandler(t, nil)
	outcome := handler.handle(context.Background(), CallerMCPFacade, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-integration-api",`+
			`"method":"ApplyIntegrationCandidate","payload":{`+
			`"initiativeHandle":"initiative-api","integrationTaskHandle":"task-integration",`+
			`"candidateTaskHandle":"task-candidate","candidateHead":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",`+
			`"expectedIntegrationHead":"cccccccccccccccccccccccccccccccccccccccc"}}`,
	))
	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable || !outcome.Error.Retryable {
		t.Fatalf("absent integration surface outcome = %#v", outcome)
	}
}

func TestServerClientAppliesExactCandidateWithoutProjectingHostPaths(t *testing.T) {
	completedAt := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	integrations := &apiIntegrations{result: application.IntegrationApplicationResult{
		OperationID: "operation-integration-client", InitiativeHandle: "initiative-api",
		IntegrationTaskHandle: "task-integration",
		Candidate: application.IntegrationCandidateReference{
			TaskHandle: "task-candidate", RepositoryID: "repo-api", WorktreePath: "/private/worktrees/candidate",
			BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40),
			EvidenceDigest: strings.Repeat("e", 64),
		},
		Strategy: application.IntegrationMerge, Outcome: application.IntegrationApplied,
		PreviousHead: strings.Repeat("c", 40), ResultingHead: strings.Repeat("d", 40),
		StateVersion: 31, CompletedAt: completedAt,
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Integrations: integrations,
		ServiceInstanceID: "service-instance-api", Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(startHandlerServer(t, handler, CallerMCPFacade), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := ApplyIntegrationCandidateInput{
		InitiativeHandle: "initiative-api", IntegrationTaskHandle: "task-integration",
		CandidateTaskHandle: "task-candidate", CandidateHead: strings.Repeat("b", 40),
		ExpectedIntegrationHead: strings.Repeat("c", 40),
	}
	result, err := client.ApplyIntegrationCandidate(context.Background(), "operation-integration-client", input)
	if err != nil {
		t.Fatalf("ApplyIntegrationCandidate() error = %v", err)
	}
	if integrations.command.OperationID != "operation-integration-client" ||
		integrations.command.InitiativeHandle != input.InitiativeHandle ||
		integrations.command.CandidateHead != input.CandidateHead || result.StateVersion != 31 ||
		result.Outcome != application.IntegrationApplied || result.CompletedAtMs != completedAt.UnixMilli() ||
		result.SideEffect != SideEffectMutate {
		t.Fatalf("integration command/result = %#v / %#v", integrations.command, result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "worktree") || strings.Contains(string(encoded), "baseRevision") {
		t.Fatalf("integration projection exposes host authority: %s", encoded)
	}
}

func TestServerClientCarriesSeparateIntegrationRecoveryIdentity(t *testing.T) {
	completedAt := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	integrations := &apiIntegrations{result: application.IntegrationApplicationResult{
		OperationID: "operation-integration-resolution", RecoveryOperationID: "operation-integration-conflict",
		InitiativeHandle: "initiative-api", IntegrationTaskHandle: "task-integration",
		Candidate: application.IntegrationCandidateReference{
			TaskHandle: "task-candidate", RepositoryID: "repo-api", WorktreePath: "/private/worktrees/candidate",
			BaseRevision: strings.Repeat("a", 40), HeadRevision: strings.Repeat("b", 40),
			EvidenceDigest: strings.Repeat("e", 64),
		},
		Strategy: application.IntegrationRebase, Outcome: application.IntegrationApplied,
		PreviousHead: strings.Repeat("c", 40), ResultingHead: strings.Repeat("d", 40),
		StateVersion: 32, CompletedAt: completedAt,
	}}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Integrations: integrations,
		ServiceInstanceID: "service-instance-api", Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(startHandlerServer(t, handler, CallerMCPFacade), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	input := ApplyIntegrationCandidateInput{
		InitiativeHandle: "initiative-api", RecoveryOperationID: "operation-integration-conflict",
		IntegrationTaskHandle: "task-integration", CandidateTaskHandle: "task-candidate",
		CandidateHead: strings.Repeat("b", 40), ExpectedIntegrationHead: strings.Repeat("c", 40),
	}
	result, err := client.ApplyIntegrationCandidate(context.Background(), "operation-integration-resolution", input)
	if err != nil || integrations.command.OperationID != "operation-integration-resolution" ||
		integrations.command.RecoveryOperationID != input.RecoveryOperationID ||
		result.OperationID != "operation-integration-resolution" || result.RecoveryOperationID != input.RecoveryOperationID {
		t.Fatalf("ApplyIntegrationCandidate(recovery) = %#v, %v; command=%#v", result, err, integrations.command)
	}
}

func TestIntegrationApplicationBoundaryRejectsBroadenedInputAndIncompleteResult(t *testing.T) {
	integrations := &apiIntegrations{}
	handler, err := NewHandler(HandlerConfig{
		Queries: &apiQueries{}, Integrations: integrations,
		ServiceInstanceID: "service-instance-api", Clock: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := `{"protocolVersion":"` + ProtocolVersion + `","operationId":"operation-integration-boundary",` +
		`"method":"ApplyIntegrationCandidate","payload":{` +
		`"initiativeHandle":"initiative-api","integrationTaskHandle":"task-integration",` +
		`"candidateTaskHandle":"task-candidate","candidateHead":"` + strings.Repeat("b", 40) + `",` +
		`"expectedIntegrationHead":"` + strings.Repeat("c", 40) + `"`
	broadened := handler.handle(context.Background(), CallerMCPFacade, []byte(request+`,"worktreePath":"/forged"}}`))
	if broadened.Error == nil || broadened.Error.Code != domain.ErrorInvalidArgument {
		t.Fatalf("broadened integration outcome = %#v", broadened)
	}
	incomplete := handler.handle(context.Background(), CallerMCPFacade, []byte(request+`}}`))
	if incomplete.Error == nil || incomplete.Error.Code != domain.ErrorInternal {
		t.Fatalf("incomplete integration outcome = %#v", incomplete)
	}
	integrations.err = errors.New("integration dependency unavailable")
	failed := handler.handle(context.Background(), CallerMCPFacade, []byte(request+`}}`))
	if failed.Error == nil || failed.Error.Code != domain.ErrorInternal {
		t.Fatalf("failed integration outcome = %#v", failed)
	}
}

func TestIntegrationApplicationResultValidationCoversClosedOutcomesAndConflictPaths(t *testing.T) {
	input := ApplyIntegrationCandidateInput{
		InitiativeHandle: "initiative-api", IntegrationTaskHandle: "task-integration",
		CandidateTaskHandle: "task-candidate", CandidateHead: strings.Repeat("b", 40),
		ExpectedIntegrationHead: strings.Repeat("c", 40),
	}
	base := application.IntegrationApplicationResult{
		OperationID: "operation-integration-result", InitiativeHandle: input.InitiativeHandle,
		IntegrationTaskHandle: input.IntegrationTaskHandle,
		Candidate: application.IntegrationCandidateReference{
			TaskHandle: input.CandidateTaskHandle, RepositoryID: "repo-api",
			BaseRevision: strings.Repeat("a", 40), HeadRevision: input.CandidateHead,
			EvidenceDigest: strings.Repeat("e", 64),
		},
		Strategy: application.IntegrationMerge, Outcome: application.IntegrationApplied,
		PreviousHead: input.ExpectedIntegrationHead, ResultingHead: strings.Repeat("d", 40),
		StateVersion: 2, CompletedAt: time.Date(2026, time.August, 20, 13, 0, 0, 0, time.UTC),
	}
	if !validIntegrationApplicationResult(base, base.OperationID, input) {
		t.Fatal("valid applied result was rejected")
	}
	invalidIdentity := base
	invalidIdentity.OperationID = "operation-other"
	if validIntegrationApplicationResult(invalidIdentity, base.OperationID, input) {
		t.Fatal("altered result identity was accepted")
	}
	invalidStrategy := base
	invalidStrategy.Strategy = application.IntegrationStrategy("unknown")
	if validIntegrationApplicationResult(invalidStrategy, base.OperationID, input) {
		t.Fatal("unknown integration strategy was accepted")
	}
	conflicted := base
	conflicted.Outcome = application.IntegrationConflicted
	conflicted.ResultingHead = ""
	conflicted.ConflictPaths = []string{"a.txt"}
	if !validIntegrationApplicationResult(conflicted, base.OperationID, input) {
		t.Fatal("valid conflicted result was rejected")
	}
	invalidated := base
	invalidated.Outcome = application.IntegrationInvalidated
	invalidated.ResultingHead = ""
	if !validIntegrationApplicationResult(invalidated, base.OperationID, input) {
		t.Fatal("valid invalidated result was rejected")
	}
	aborted := base
	aborted.Outcome = application.IntegrationAborted
	aborted.ResultingHead = ""
	if !validIntegrationApplicationResult(aborted, base.OperationID, input) {
		t.Fatal("valid aborted result was rejected")
	}
	unknown := base
	unknown.Outcome = application.IntegrationOutcome("unknown")
	if validIntegrationApplicationResult(unknown, base.OperationID, input) {
		t.Fatal("unknown integration outcome was accepted")
	}
	for _, paths := range [][]string{nil, {"z.txt", "a.txt"}, {"a.txt", "a.txt"}, {"../escape"}} {
		if validIntegrationConflictPaths(paths) {
			t.Fatalf("invalid conflict paths were accepted: %#v", paths)
		}
	}
}

type apiIntegrations struct {
	command application.ApplyIntegrationCandidateCommand
	result  application.IntegrationApplicationResult
	err     error
}

func (integrations *apiIntegrations) ApplyCandidate(
	_ context.Context,
	command application.ApplyIntegrationCandidateCommand,
) (application.IntegrationApplicationResult, error) {
	integrations.command = command
	return integrations.result, integrations.err
}
