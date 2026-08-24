package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestIntegrationReservationClaimsAndReconcilesGlobalOperationLedger(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-ledger-claim", application.IntegrationRebase)
	reserved, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := fixture.store.GetOperation(context.Background(), request.Command.OperationID)
	if err != nil || accepted.Status != domain.OperationAccepted || accepted.Command != commandApplyIntegrationCandidate ||
		accepted.SubjectDigest != request.SubjectDigest || accepted.ResultRef != request.Command.IntegrationTaskHandle ||
		!accepted.CreatedAt.Equal(request.At) || !accepted.UpdatedAt.Equal(request.At) {
		t.Fatalf("accepted integration operation = %#v, %v", accepted, err)
	}
	collision := storeOperation(request.Command.OperationID, accepted.StateVersion+1)
	if err := fixture.store.RecordOperation(context.Background(), collision); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("RecordOperation(colliding command) error = %v", err)
	}

	reconcileAt := request.At.Add(time.Minute)
	if _, err := fixture.store.ReconcileStartup(context.Background(), reconcileAt); err != nil {
		t.Fatalf("ReconcileStartup() error = %v", err)
	}
	unknown, err := fixture.store.GetOperation(context.Background(), request.Command.OperationID)
	if err != nil || unknown.Status != domain.OperationUnknown || unknown.ID != accepted.ID ||
		unknown.Command != accepted.Command || unknown.SubjectDigest != accepted.SubjectDigest ||
		unknown.ResultRef != accepted.ResultRef || !unknown.CreatedAt.Equal(accepted.CreatedAt) ||
		!unknown.UpdatedAt.Equal(reconcileAt) {
		t.Fatalf("reconciled integration operation = %#v, %v", unknown, err)
	}
	replayed, err := fixture.store.ReserveIntegrationApplication(context.Background(), request)
	if err != nil || replayed.OperationID != reserved.OperationID || replayed.Result != nil {
		t.Fatalf("ReserveIntegrationApplication(reconciled replay) = %#v, %v", replayed, err)
	}
	completed, err := fixture.store.CompleteIntegrationApplication(context.Background(), application.IntegrationCompletion{
		Reservation: replayed,
		AdapterResult: application.IntegrationAdapterResult{
			Outcome: application.IntegrationApplied, PreviousHead: replayed.Target.ExpectedHead,
			ResultingHead: strings.Repeat("d", 40),
		},
		At: reconcileAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	done, err := fixture.store.GetOperation(context.Background(), request.Command.OperationID)
	if err != nil || done.Status != domain.OperationCompleted || done.ID != accepted.ID ||
		done.StateVersion != completed.StateVersion || !done.CreatedAt.Equal(accepted.CreatedAt) ||
		!done.UpdatedAt.Equal(completed.CompletedAt) {
		t.Fatalf("completed integration operation = %#v, %v", done, err)
	}
}

func TestIntegrationReservationRejectsMissingOrAlteredLedgerClaim(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*storedIntegrationFixture, string)
	}{
		{name: "missing", mutate: func(fixture *storedIntegrationFixture, operationID string) {
			mustExecIntegrationTest(t, fixture, `DELETE FROM operations WHERE id = ?`, operationID)
		}},
		{name: "altered", mutate: func(fixture *storedIntegrationFixture, operationID string) {
			mustExecIntegrationTest(t, fixture, `UPDATE operations SET command = 'PrepareTask' WHERE id = ?`, operationID)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoredIntegrationFixture(t)
			request := fixture.reservationRequest("integration-ledger-"+test.name, application.IntegrationMerge)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			test.mutate(&fixture, request.Command.OperationID)
			if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); err == nil {
				t.Fatal("ReserveIntegrationApplication(corrupt ledger) error = nil")
			}
		})
	}
}
