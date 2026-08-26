package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/validation"
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

func TestValidationReceiptMismatchUsesClosedContentFreeCodes(t *testing.T) {
	fixture := newCandidateSupervisorFixture(t, domain.ShapeShip)
	profile, err := fixture.catalog.ResolveProfile(fixture.task.ValidationProfile)
	if err != nil {
		t.Fatalf("ResolveProfile() error = %v", err)
	}
	check := profile.LocalChecks[0]
	base := fixture.runner.receipt
	tests := []struct {
		name   string
		want   string
		mutate func(*validation.Receipt)
	}{
		{name: "complete receipt", want: "", mutate: func(*validation.Receipt) {}},
		{name: "operation identity", want: "operation_id", mutate: func(receipt *validation.Receipt) { receipt.OperationID = "other-operation" }},
		{name: "task handle", want: "task_handle", mutate: func(receipt *validation.Receipt) { receipt.TaskHandle = "task-other" }},
		{name: "profile identity", want: "profile_id", mutate: func(receipt *validation.Receipt) { receipt.ProfileID = "profile-other" }},
		{name: "check identity", want: "check_id", mutate: func(receipt *validation.Receipt) { receipt.CheckID = "check-other" }},
		{name: "program identity", want: "program_id", mutate: func(receipt *validation.Receipt) { receipt.ProgramID = "program-other" }},
		{name: "head revision", want: "head_revision", mutate: func(receipt *validation.Receipt) { receipt.HeadRevision = strings.Repeat("f", 40) }},
		{name: "start timezone", want: "started_at_timezone", mutate: func(receipt *validation.Receipt) {
			receipt.StartedAt = receipt.StartedAt.In(time.FixedZone("offset", 3600))
		}},
		{name: "completion timezone", want: "completed_at_timezone", mutate: func(receipt *validation.Receipt) {
			receipt.CompletedAt = receipt.CompletedAt.In(time.FixedZone("offset", 3600))
		}},
		{name: "completion order", want: "completion_order", mutate: func(receipt *validation.Receipt) { receipt.CompletedAt = receipt.StartedAt.Add(-time.Second) }},
		{name: "output hash length", want: "output_hash_length", mutate: func(receipt *validation.Receipt) { receipt.OutputHash = "short" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receipt := base
			test.mutate(&receipt)
			if got := validationReceiptMismatch(
				receipt, base.OperationID, fixture.task, profile, check, fixture.snapshot,
			); got != test.want {
				t.Fatalf("validationReceiptMismatch() = %q, want %q", got, test.want)
			}
		})
	}
}
