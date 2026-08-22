package service

import (
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestCandidateSupervisorNamesIncompleteValidationReceiptField(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	fixture.runner.receipt.ProfileID = "wrong-profile"
	supervisor, err := newCandidateSupervisor(fixture.config())
	if err != nil {
		t.Fatalf("newCandidateSupervisor() error = %v", err)
	}
	profile, err := fixture.catalog.ResolveProfile(fixture.task.ValidationProfile)
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}
	_, _, err = supervisor.runLocalChecks(context.Background(), fixture.task, profile, fixture.snapshot)
	if err == nil || !strings.Contains(err.Error(), "profile_id") {
		t.Fatalf("runLocalChecks() error = %v, want content-free profile_id mismatch", err)
	}
}
