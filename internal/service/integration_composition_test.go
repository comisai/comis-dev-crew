package service

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestIntegrationCompositionRequiresClosedReviewedPolicyAndCompleteDependencies(t *testing.T) {
	configured := map[string]application.IntegrationStrategy{
		"integration-merge":  application.IntegrationMerge,
		"integration-rebase": application.IntegrationRebase,
		"integration-cherry": application.IntegrationCherryPick,
	}
	resolver, err := newIntegrationPolicyResolver(configured)
	if err != nil {
		t.Fatal(err)
	}
	configured["integration-merge"] = application.IntegrationStrategy("changed")
	strategy, err := resolver("integration-merge")
	if err != nil || strategy != application.IntegrationMerge {
		t.Fatalf("resolver(merge) = %q, %v", strategy, err)
	}
	if _, err := resolver("integration-unreviewed"); err == nil {
		t.Fatal("resolver(unreviewed) error = nil")
	}
	for _, policies := range []map[string]application.IntegrationStrategy{
		nil,
		{"bad policy": application.IntegrationMerge},
		{"integration-default": application.IntegrationStrategy("reset")},
	} {
		if _, err := newIntegrationPolicyResolver(policies); err == nil {
			t.Fatalf("newIntegrationPolicyResolver(%#v) error = nil", policies)
		}
	}
	clock := func() time.Time { return time.Date(2026, time.August, 20, 14, 0, 0, 0, time.UTC) }
	if integrations, err := composeIntegrationApplications(Config{}, nil, clock); err != nil || integrations != nil {
		t.Fatalf("composeIntegrationApplications(empty) = %#v, %v", integrations, err)
	}
	if _, err := composeIntegrationApplications(Config{IntegrationPolicies: resolver}, serviceIntegrationStore{}, clock); err == nil {
		t.Fatal("composeIntegrationApplications(partial) error = nil")
	}
	integrations, err := composeIntegrationApplications(Config{
		IntegrationPolicies: resolver, integrationAdapter: serviceIntegrationAdapter{},
	}, serviceIntegrationStore{}, clock)
	if err != nil || integrations == nil {
		t.Fatalf("composeIntegrationApplications(complete) = %#v, %v", integrations, err)
	}
}

type serviceIntegrationStore struct{}

func (serviceIntegrationStore) IntegrationPolicy(context.Context, string) (string, error) {
	return "integration-merge", nil
}

func (serviceIntegrationStore) ReserveIntegrationApplication(
	context.Context,
	application.IntegrationReservationRequest,
) (application.ReservedIntegrationApplication, error) {
	return application.ReservedIntegrationApplication{}, nil
}

func (serviceIntegrationStore) CompleteIntegrationApplication(
	context.Context,
	application.IntegrationCompletion,
) (application.IntegrationApplicationResult, error) {
	return application.IntegrationApplicationResult{}, nil
}

type serviceIntegrationAdapter struct{}

func (serviceIntegrationAdapter) ApplyIntegrationCandidate(
	context.Context,
	application.IntegrationAdapterRequest,
) (application.IntegrationAdapterResult, error) {
	return application.IntegrationAdapterResult{}, nil
}
