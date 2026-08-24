package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestIntegrationReservationRejectsWorkingOwnerWithActiveWriter(t *testing.T) {
	fixture := newStoredIntegrationFixture(t)
	request := fixture.reservationRequest("integration-active-writer-refusal", application.IntegrationMerge)

	if _, err := fixture.store.ReserveIntegrationApplication(context.Background(), request); !errors.Is(err, application.ErrPrecondition) {
		t.Fatalf("ReserveIntegrationApplication(working owner) error = %v", err)
	}
	var count int
	if err := fixture.store.db.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM integration_applications`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("integration reservations after refusal = %d, %v", count, err)
	}
}
