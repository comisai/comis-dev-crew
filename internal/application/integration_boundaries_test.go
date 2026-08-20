package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIntegrationCompositionRequiresEveryAuthority(t *testing.T) {
	base := IntegrationConfig{
		Store: &integrationStore{}, Adapter: &integrationAdapter{},
		Policies: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil },
		Clock:    func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	}
	tests := []struct {
		name   string
		mutate func(*IntegrationConfig)
	}{
		{name: "store", mutate: func(config *IntegrationConfig) { config.Store = nil }},
		{name: "adapter", mutate: func(config *IntegrationConfig) { config.Adapter = nil }},
		{name: "policies", mutate: func(config *IntegrationConfig) { config.Policies = nil }},
		{name: "clock", mutate: func(config *IntegrationConfig) { config.Clock = nil }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := base
			test.mutate(&config)
			if _, err := NewIntegrations(config); err == nil {
				t.Fatal("NewIntegrations() error = nil")
			}
		})
	}
}

func TestIntegrationCoordinatorFailsClosedAcrossDependencyBoundaries(t *testing.T) {
	at := time.Unix(1_800_000_000, 0).UTC()
	command := integrationCommand()
	adapterFailure := errors.New("adapter unavailable")
	storeFailure := errors.New("store unavailable")
	tests := []struct {
		name        string
		store       *integrationStore
		adapter     *integrationAdapter
		policy      IntegrationPolicyResolver
		clock       Clock
		wantAdapter bool
	}{
		{name: "policy read", store: &integrationStore{policyErr: storeFailure}, adapter: &integrationAdapter{},
			policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }},
		{name: "policy resolution", store: &integrationStore{policyID: "integration-reviewed"}, adapter: &integrationAdapter{},
			policy: func(string) (IntegrationStrategy, error) { return "", storeFailure }},
		{name: "unknown policy strategy", store: &integrationStore{policyID: "integration-reviewed"}, adapter: &integrationAdapter{},
			policy: func(string) (IntegrationStrategy, error) { return "shell_fragment", nil }},
		{name: "reservation", store: &integrationStore{policyID: "integration-reviewed", reserveErr: storeFailure}, adapter: &integrationAdapter{},
			policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }},
		{name: "reservation differs", store: &integrationStore{
			policyID: "integration-reviewed", reservation: func() ReservedIntegrationApplication {
				reserved := integrationReservation(command, IntegrationMerge)
				reserved.Candidate.HeadRevision = strings.Repeat("f", 40)
				return reserved
			}(),
		}, adapter: &integrationAdapter{}, policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }},
		{name: "adapter", store: &integrationStore{policyID: "integration-reviewed", reservation: integrationReservation(command, IntegrationMerge)},
			adapter: &integrationAdapter{err: adapterFailure}, policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }, wantAdapter: true},
		{name: "completion", store: &integrationStore{
			policyID: "integration-reviewed", reservation: integrationReservation(command, IntegrationMerge), completeErr: storeFailure,
		}, adapter: &integrationAdapter{result: IntegrationAdapterResult{
			Outcome: IntegrationApplied, PreviousHead: command.ExpectedIntegrationHead, ResultingHead: strings.Repeat("d", 40),
		}}, policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }, wantAdapter: true},
		{name: "completion differs", store: &integrationStore{
			policyID: "integration-reviewed", reservation: integrationReservation(command, IntegrationMerge),
		}, adapter: &integrationAdapter{result: IntegrationAdapterResult{
			Outcome: IntegrationApplied, PreviousHead: command.ExpectedIntegrationHead, ResultingHead: strings.Repeat("d", 40),
		}}, policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }, wantAdapter: true},
		{name: "invalid clock", store: &integrationStore{policyID: "integration-reviewed"}, adapter: &integrationAdapter{},
			policy: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil }, clock: func() time.Time { return time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := test.clock
			if clock == nil {
				clock = func() time.Time { return at }
			}
			integrations, err := NewIntegrations(IntegrationConfig{
				Store: test.store, Adapter: test.adapter, Policies: test.policy, Clock: clock,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := integrations.ApplyCandidate(context.Background(), command); err == nil {
				t.Fatal("ApplyCandidate() error = nil")
			}
			if test.wantAdapter != (len(test.adapter.requests) == 1) {
				t.Fatalf("adapter calls = %d", len(test.adapter.requests))
			}
		})
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	integrations, err := NewIntegrations(IntegrationConfig{
		Store: &integrationStore{}, Adapter: &integrationAdapter{},
		Policies: func(string) (IntegrationStrategy, error) { return IntegrationMerge, nil },
		Clock:    func() time.Time { return at },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := integrations.ApplyCandidate(cancelled, command); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyCandidate(cancelled) error = %v", err)
	}
}
