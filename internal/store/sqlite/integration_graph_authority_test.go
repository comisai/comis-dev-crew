package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestIntegrationReservationRequiresExactCandidateEdge(t *testing.T) {
	fixture := newStoredIntegrationFixtureWithMutation(t, func(mutation *application.PreparedInitiativeMutation) {
		mutation.Initiative.Edges = nil
	})
	request := fixture.reservationRequest("integration-unrelated-candidate", application.IntegrationMerge)

	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReserveIntegrationApplication(unrelated candidate) error = %v, want ErrPrecondition", err)
	}
	assertNoIntegrationReservations(t, fixture.store)
}

func TestIntegrationReservationRequiresEveryPredecessorReady(t *testing.T) {
	fixture := newStoredIntegrationFixtureWithMutation(t, func(mutation *application.PreparedInitiativeMutation) {
		second := mutation.Members[0]
		second.Task.Handle = "task-component-b"
		second.Task.ManagedRunID = ""
		second.Task.WorkspaceLeaseID = ""
		second.Task.State = domain.TaskPrepared
		second.Task.BriefRevisionHash = ""
		var err error
		second.Task, err = second.Task.PinBriefRevision()
		if err != nil {
			t.Fatal(err)
		}
		second.OperationID = "prepare-member-0003"
		second.SubjectDigest = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		second.Preparation.ExternalRunRef = second.Task.Handle
		second.Preparation.RegistrationNonce = "registration-nonce_" + second.Task.Handle
		second.Preparation.RequestedWorkspaceRoot = "/approved/workspaces/" + second.Task.Handle
		second.Preparation.RequestedAttachment.SourcePath = "/approved/runtime/" + second.Task.Handle + "/attachment.sock"
		mutation.Members = append(mutation.Members, second)
		mutation.Initiative.Components[0].TaskHandles = append(
			mutation.Initiative.Components[0].TaskHandles, second.Task.Handle,
		)
		mutation.Initiative.Edges = append(mutation.Initiative.Edges, domain.InitiativeEdge{
			FromTaskHandle: second.Task.Handle,
			ToTaskHandle:   mutation.Initiative.IntegrationOwnerTask,
			Kind:           domain.EdgeIntegratesAfter,
		})
	})
	request := fixture.reservationRequest("integration-incomplete-predecessor", application.IntegrationMerge)

	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReserveIntegrationApplication(incomplete predecessor) error = %v, want ErrPrecondition", err)
	}
	assertNoIntegrationReservations(t, fixture.store)
}

func assertNoIntegrationReservations(t *testing.T, store *Store) {
	t.Helper()
	var count int
	if err := store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM integration_applications`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("integration reservations = %d, %v", count, err)
	}
}
