package localapi

import (
	"context"
	"testing"

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
