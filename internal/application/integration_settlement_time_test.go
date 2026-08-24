package application

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIntegrationSamplesPreMutationSettlementTimeAfterAdapterFailure(t *testing.T) {
	command := integrationCommand()
	reserved := integrationReservation(command, IntegrationRebase)
	reservedAt := time.Unix(1_800_000_000, 0).UTC()
	settledAt := reservedAt.Add(3 * time.Minute)
	reserved.ReservedAt = reservedAt
	reserved.EvidenceExpiresAt = settledAt.Add(time.Hour)
	store := &integrationStore{
		policyID: "integration-reviewed", reservation: reserved,
		completed: integrationResult(reserved, IntegrationInvalidated, "", nil, settledAt),
	}
	adapter := &integrationAdapter{err: errors.Join(
		errors.New("candidate proof exceeded its bound"), ErrIntegrationMutationNotStarted,
	)}
	clockCalls := 0
	integrations, err := NewIntegrations(IntegrationConfig{
		Store: store, Adapter: adapter,
		Policies: func(string) (IntegrationStrategy, error) { return IntegrationRebase, nil },
		Clock: func() time.Time {
			clockCalls++
			if clockCalls == 1 {
				return reservedAt
			}
			return settledAt
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := integrations.ApplyCandidate(context.Background(), command); err == nil {
		t.Fatal("ApplyCandidate(pre-mutation failure) error = nil")
	}
	if clockCalls != 2 || !store.completion.At.Equal(settledAt) {
		t.Fatalf("settlement clock calls = %d, completion time = %v, want %v", clockCalls, store.completion.At, settledAt)
	}
}
