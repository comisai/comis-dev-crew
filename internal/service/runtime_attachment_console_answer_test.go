package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func (store *runtimeAttachmentRecoveryStore) ReadDecisionResponseForManagedRun(
	_ context.Context,
	managedRunID string,
	externalKey string,
) (application.DecisionResponse, bool, error) {
	answer, found := store.decisionAnswers[managedRunID+":"+externalKey]
	if !found {
		return application.DecisionResponse{}, false, nil
	}
	return application.DecisionResponse{ExternalKey: externalKey, Response: answer}, true, nil
}

// The console answers a question so the work can continue while the channel that
// raised it is unavailable. If reaching Comis were still required to hand that
// answer to the worker, the console could record an answer that never arrives
// anywhere, which is the outage it exists to survive.
func TestRuntimeAttachmentCoordinator_ServesAConsoleAnswerWhileComisIsUnreachable(t *testing.T) {
	root := shortTempDir(t)
	coordinator, err := newRuntimeAttachmentCoordinator(runtimeAttachmentCoordinatorConfig{
		RuntimeRoot: filepath.Join(root, "runtime"),
		Store: &runtimeAttachmentRecoveryStore{
			decisionAnswers: map[string]string{"managed-run.attention:database-choice": "use the existing adapter"},
		},
		Clock:                   time.Now,
		NewCredential:           func() (string, error) { return "attention-credential-0123456789abcdef", nil },
		NewAttentionOperationID: runtimeAttentionOperationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	// No response receiver is configured, so Comis is unreachable by construction.
	response, err := coordinator.ReceiveRuntimeAttentionResponse(context.Background(), reporter.AttentionResponseRequest{
		OperationID: "attention-response-console", ManagedRunID: "managed-run.attention", ExternalKey: "database-choice",
	})
	if err != nil {
		t.Fatalf("ReceiveRuntimeAttentionResponse(console answer) error = %v", err)
	}
	if response.State != reporter.AttentionResponseDelivered || response.Response != "use the existing adapter" {
		t.Fatalf("console answer served = %#v", response)
	}
}
