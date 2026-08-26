package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestPendingIntegrationCanBeResumedByFreshAuthorizedOperation(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	originalRequest := fixture.reservationRequest("integration-pending-store-original", application.IntegrationMerge)
	original, err := fixture.store.ReserveIntegrationApplication(context.Background(), originalRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.db.Exec(`UPDATE integration_applications SET evidence_expires_at = ? WHERE operation_id = ?`,
		originalRequest.At.Add(time.Second).Format(time.RFC3339Nano), original.OperationID); err != nil {
		t.Fatal(err)
	}
	resume := fixture.reservationRequest("integration-pending-store-resume", application.IntegrationMerge)
	resume.Command.RecoveryOperationID = original.OperationID
	resume.At = originalRequest.At.Add(2 * time.Second)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), resume)
	if err != nil || reserved.OperationID != resume.Command.OperationID ||
		reserved.RecoveryOperationID != original.OperationID || reserved.Result != nil ||
		!reserved.ReservedAt.Equal(resume.At) || !resume.At.Before(reserved.EvidenceExpiresAt) {
		t.Fatalf("ReserveIntegrationApplication(pending resume) = %#v, %v", reserved, err)
	}
	resultingHead := "dddddddddddddddddddddddddddddddddddddddd"
	completed, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: reserved,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationApplied, PreviousHead: reserved.Target.ExpectedHead,
			ResultingHead: resultingHead,
		},
		At: resume.At.Add(time.Second),
	})
	if err != nil || completed.Outcome != application.IntegrationApplied {
		t.Fatalf("CompleteIntegrationApplication(pending resume) = %#v, %v", completed, err)
	}
	replayed, err := fixture.store.ReserveIntegrationApplication(context.Background(), originalRequest)
	if err != nil || replayed.Result == nil || replayed.Result.Outcome != application.IntegrationApplied ||
		replayed.Result.ResultingHead != resultingHead {
		t.Fatalf("ReserveIntegrationApplication(original replay) = %#v, %v", replayed, err)
	}
}
