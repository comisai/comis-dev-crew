package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestStatusNamesCapacityCountsAndSaturatedScopes(t *testing.T) {
	client := &fakeClient{fleet: application.FleetSnapshot{
		SchemaVersion: 1,
		Capacity: application.FleetCapacitySnapshot{
			Known: true,
			Dimensions: []application.FleetCapacityDimension{
				{Kind: application.CapacityHost, Used: 4, Limit: 4, Saturated: true},
				{Kind: application.CapacityRepository, ID: "product-api", Used: 4, Limit: 4, Saturated: true},
				{Kind: application.CapacityWorkerProfile, ID: "claude-reviewed", Used: 2, Limit: 2, Saturated: true},
			},
		},
	}}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"status"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(status) = %d: %s", code, output.String())
	}
	rendered := output.String()
	for _, want := range []string{
		"CAPACITY", "USED", "LIMIT", "AVAILABLE", "SATURATED",
		"host", "repository:product-api", "worker_profile:claude-reviewed",
		"4", "2", "true",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("status omitted %q: %s", want, rendered)
		}
	}
}
