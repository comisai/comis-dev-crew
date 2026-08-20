package localapi

import (
	"context"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestTaskMergeMethodRequiresCanonicalMutationSurface(t *testing.T) {
	method := Method("MergeTask")
	if !method.valid() || method.SideEffect() != SideEffectMutate ||
		!methodAllowed(CallerOperatorCLI, method) || !methodAllowed(CallerMCPFacade, method) {
		t.Fatalf("merge method posture = valid:%t sideEffect:%q operator:%t mcp:%t",
			method.valid(), method.SideEffect(), methodAllowed(CallerOperatorCLI, method), methodAllowed(CallerMCPFacade, method))
	}
	handler := newTestHandler(t, nil)
	outcome := handler.handle(context.Background(), CallerOperatorCLI, []byte(
		`{"protocolVersion":"`+ProtocolVersion+`","operationId":"operation-merge-api",`+
			`"method":"MergeTask","payload":{"taskHandle":"task-merge-api"}}`,
	))
	if outcome.Status != domain.OperationRejected || outcome.Error == nil ||
		outcome.Error.Code != domain.ErrorUnavailable || !outcome.Error.Retryable {
		t.Fatalf("absent merge surface outcome = %#v", outcome)
	}
}
