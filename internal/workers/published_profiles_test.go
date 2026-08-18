package workers_test

import (
	"reflect"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/workers"
)

// The published view is what leaves this package. Launch authority — the
// executable, its argument vectors, and the environment keys it may read — is
// reviewed once by an operator and used only to build a descriptor here. If the
// published type were StaticProfile with fields omitted at each call site, a
// field added later would leak by default; it is a distinct type so it cannot.
func TestProfileCatalog_PublishedProfilesCarryNoLaunchAuthority(t *testing.T) {
	published := reflect.TypeOf(workers.PublishedProfile{})
	for _, forbidden := range []string{
		"Executable", "ExecutableArguments", "Arguments", "EnvironmentKeys",
		"TerminalAllowEntry", "Model", "Effort", "Network",
	} {
		if _, found := published.FieldByName(forbidden); found {
			t.Errorf("PublishedProfile exposes launch authority %q", forbidden)
		}
	}
}

// "No profile accepts this shape" and "the profile that accepts it cannot run"
// are different answers to a stalled caller. A catalog that published only
// usable profiles would collapse them into one.
func TestProfileCatalog_PublishedProfilesKeepUnavailableOnesInIdentityOrder(t *testing.T) {
	executable := codexFixtureExecutable(t)
	unavailable := availableCodexProfile(executable, "profile-b")
	unavailable.Availability = workers.AvailabilityUnavailable
	unavailable.AvailabilityReason = workers.AvailabilityReasonExecutable
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{
		unavailable, availableCodexProfile(executable, "profile-a"),
	})
	if err != nil {
		t.Fatalf("new profile catalog: %v", err)
	}

	published := catalog.PublishedProfiles()

	if len(published) != 2 {
		t.Fatalf("published = %+v, want both profiles", published)
	}
	if published[0].ID != "profile-a" || published[1].ID != "profile-b" {
		t.Errorf("published order = %q, %q; want identity order", published[0].ID, published[1].ID)
	}
	if published[1].Availability != workers.AvailabilityUnavailable ||
		published[1].AvailabilityReason != workers.AvailabilityReasonExecutable {
		t.Errorf("an unavailable profile must keep its reason, got %+v", published[1])
	}
	if published[0].Harness != workers.HarnessCodex || published[0].ConcurrencyLimit != 2 {
		t.Errorf("published identity and posture = %+v", published[0])
	}
}

// Mutating a published slice must not reach the reviewed catalog: a caller that
// could rewrite the allowed shapes would be editing the operator's review.
func TestProfileCatalog_PublishedProfilesDoNotAliasTheReviewedCatalog(t *testing.T) {
	executable := codexFixtureExecutable(t)
	catalog, err := workers.NewProfileCatalog([]workers.StaticProfile{
		availableCodexProfile(executable, "profile-a"),
	})
	if err != nil {
		t.Fatalf("new profile catalog: %v", err)
	}

	published := catalog.PublishedProfiles()
	published[0].AllowedShapes[0] = domain.ShapeScout

	if _, err := catalog.ResolveProfile("profile-a", domain.ShapeShip); err != nil {
		t.Fatalf("the reviewed catalog was mutated through its published view: %v", err)
	}
}

// An absent catalog is a real deployment state, not a crash site.
func TestProfileCatalog_WhenAbsent_PublishesNothing(t *testing.T) {
	var catalog *workers.ProfileCatalog
	if published := catalog.PublishedProfiles(); published != nil {
		t.Errorf("published = %+v, want nil", published)
	}
}
