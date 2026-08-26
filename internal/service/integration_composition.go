package service

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func newIntegrationPolicyResolver(
	configured map[string]application.IntegrationStrategy,
) (application.IntegrationPolicyResolver, error) {
	if len(configured) == 0 || len(configured) > 64 {
		return nil, errors.New("reviewed integration policies are required")
	}
	policies := make(map[string]application.IntegrationStrategy, len(configured))
	for id, strategy := range configured {
		if domain.ValidateTaskHandle(id) != nil || !validIntegrationStrategy(strategy) {
			return nil, errors.New("reviewed integration policy is invalid")
		}
		policies[id] = strategy
	}
	return func(policyID string) (application.IntegrationStrategy, error) {
		strategy, found := policies[policyID]
		if !found {
			return "", errors.New("reviewed integration policy is unavailable")
		}
		return strategy, nil
	}, nil
}

func validIntegrationStrategy(strategy application.IntegrationStrategy) bool {
	return strategy == application.IntegrationMerge || strategy == application.IntegrationRebase ||
		strategy == application.IntegrationCherryPick
}

func composeIntegrationApplications(
	config Config,
	store application.IntegrationStore,
	clock application.Clock,
) (*application.Integrations, error) {
	if config.integrationAdapter == nil && config.IntegrationPolicies == nil {
		return nil, nil
	}
	if config.integrationAdapter == nil || config.IntegrationPolicies == nil {
		return nil, errors.New("run service: integration application composition is incomplete")
	}
	integrations, err := application.NewIntegrations(application.IntegrationConfig{
		Store: store, Adapter: config.integrationAdapter, Policies: config.IntegrationPolicies, Clock: clock,
	})
	if err != nil {
		return nil, errors.New("run service: integration application composition is invalid")
	}
	return integrations, nil
}
