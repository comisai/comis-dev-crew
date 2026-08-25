package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestIntegrationAbortedRecoveryReleasesConflictForCorrectedOperation(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := storedRebaseRecoveryRequest(t, &fixture)
	first, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	aborted, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: first,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationAborted, PreviousHead: first.Target.ExpectedHead,
		},
		At: request.At.Add(time.Second),
	})
	if err != nil || aborted.Outcome != application.IntegrationAborted {
		t.Fatalf("CompleteIntegrationApplication(aborted recovery) = %#v, %v", aborted, err)
	}
	replayed, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil || replayed.Result == nil || replayed.Result.Outcome != application.IntegrationAborted {
		t.Fatalf("ReserveIntegrationApplication(aborted replay) = %#v, %v", replayed, err)
	}
	corrected := request
	corrected.Command.OperationID = "integration-rebase-authority-corrected"
	corrected.SubjectDigest = strings.Repeat("7", 64)
	corrected.At = request.At.Add(2 * time.Second)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), corrected)
	if err != nil || reserved.OperationID != corrected.Command.OperationID || reserved.Result != nil {
		t.Fatalf("ReserveIntegrationApplication(corrected recovery) = %#v, %v", reserved, err)
	}
	duplicate := corrected
	duplicate.Command.OperationID = "integration-rebase-authority-duplicate"
	duplicate.SubjectDigest = strings.Repeat("6", 64)
	duplicate.At = corrected.At.Add(time.Second)
	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), duplicate); !errors.Is(err, application.ErrIntegrationApplicationExists) {
		t.Fatalf("ReserveIntegrationApplication(nonterminal duplicate) error = %v", err)
	}
}
