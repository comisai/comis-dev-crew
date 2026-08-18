package service

import (
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

// The catalog read exists to separate three answers a preparation failure
// collapses into one, so the mapping must carry the availability reason and the
// unattended posture through — not just the profile's name.
func TestPublishedWorkerProfileSummaries_CarriesPostureNotJustIdentity(t *testing.T) {
	summaries := publishedWorkerProfileSummaries([]workers.PublishedProfile{
		{
			ID: "codex-reviewed", Harness: workers.HarnessCodex,
			AllowedShapes: []domain.TaskShape{domain.ShapeShip, domain.ShapeScout},
			Availability:  workers.AvailabilityAvailable,
			Unattended:    true, ConcurrencyLimit: 2,
		},
		{
			ID: "claude-reviewed", Harness: workers.HarnessClaude,
			AllowedShapes:      []domain.TaskShape{domain.ShapeShip},
			Availability:       workers.AvailabilityUnavailable,
			AvailabilityReason: workers.AvailabilityReasonExecutable,
		},
	})

	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
	if summaries[0].ProfileID != "codex-reviewed" || summaries[0].Harness != "codex" ||
		!summaries[0].Unattended || summaries[0].ConcurrencyLimit != 2 ||
		len(summaries[0].AllowedShapes) != 2 {
		t.Errorf("available profile mapped as %+v", summaries[0])
	}
	if summaries[1].Availability != "unavailable" ||
		summaries[1].AvailabilityReason != "executable_unavailable" {
		t.Errorf("an unavailable profile must keep its reason, got %+v", summaries[1])
	}
	if summaries[1].Unattended {
		t.Error("posture must be mapped per profile, not inherited from a sibling")
	}
}

// A deployment with no published profile reports an empty catalog, which is the
// honest answer to "what can run here" — never a nil the read layer must guess
// about.
func TestPublishedWorkerProfileSummaries_WhenNothingIsPublished_IsEmptyNotNil(t *testing.T) {
	summaries := publishedWorkerProfileSummaries(nil)
	if summaries == nil || len(summaries) != 0 {
		t.Errorf("summaries = %+v, want empty and non-nil", summaries)
	}
}
