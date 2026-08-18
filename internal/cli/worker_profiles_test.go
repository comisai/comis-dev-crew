package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func workerProfileFixture() application.WorkerProfileList {
	return application.WorkerProfileList{
		SchemaVersion: 1, StateVersion: 7,
		Profiles: []application.WorkerProfileSummary{
			{
				ProfileID: "codex-reviewed", Harness: "codex",
				AllowedShapes: []domain.TaskShape{domain.ShapeShip},
				Availability:  "available", Unattended: true, ConcurrencyLimit: 2,
			},
			{
				ProfileID: "claude-reviewed", Harness: "claude",
				AllowedShapes: []domain.TaskShape{domain.ShapeShip, domain.ShapeScout},
				Availability:  "unavailable", AvailabilityReason: "executable_unavailable",
			},
		},
	}
}

// An operator asking "why can nothing start" must get the reason in the listing
// itself. A row that said only "unavailable" would force a second command to
// learn what it meant, which is the friction this read exists to remove.
func TestCLI_WhenAProfileIsUnavailable_TheListingNamesTheReasonInline(t *testing.T) {
	client := &fakeClient{profiles: workerProfileFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"workers", "list"}, &output, &output, testConfig(client)); code != 0 {
		t.Fatalf("Run(workers list) = %d: %s", code, output.String())
	}

	rendered := output.String()
	if !strings.Contains(rendered, "executable_unavailable") {
		t.Errorf("listing omitted the availability reason: %s", rendered)
	}
	if !strings.Contains(rendered, "ship,scout") {
		t.Errorf("listing omitted the shapes a profile accepts: %s", rendered)
	}
	if !strings.Contains(rendered, "codex-reviewed") || !strings.Contains(rendered, "claude-reviewed") {
		t.Errorf("listing omitted a configured profile: %s", rendered)
	}
}

// The table is a projection; JSON is the machine surface. Neither may carry the
// executable, its arguments, or the environment keys — those are launch
// authority and never leave the workers package.
func TestCLI_TheWorkerListingCarriesNoLaunchAuthority(t *testing.T) {
	client := &fakeClient{profiles: workerProfileFixture()}
	for _, format := range []string{"table", "json"} {
		var output bytes.Buffer
		if code := Run(context.Background(), []string{"workers", "list", "--format", format}, &output, &output, testConfig(client)); code != 0 {
			t.Fatalf("Run(workers list --format %s) = %d: %s", format, code, output.String())
		}
		rendered := strings.ToLower(output.String())
		for _, forbidden := range []string{"executable\"", "argv", "environmentkeys", "/usr/", "api_key"} {
			if strings.Contains(rendered, forbidden) {
				t.Errorf("%s output leaked launch authority %q: %s", format, forbidden, rendered)
			}
		}
	}
}

func TestCLI_WhenTheWorkersSubcommandIsWrong_RefusesRatherThanGuess(t *testing.T) {
	client := &fakeClient{profiles: workerProfileFixture()}
	var output bytes.Buffer

	if code := Run(context.Background(), []string{"workers"}, &output, &output, testConfig(client)); code == 0 {
		t.Fatalf("Run(workers) = 0, want a refusal: %s", output.String())
	}
}
