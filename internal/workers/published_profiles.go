package workers

import (
	"sort"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// PublishedProfile is what a reviewed profile may reveal outside this package.
//
// A StaticProfile also carries the executable, its argument vectors, and the
// environment keys it may read. Those are launch authority: they are reviewed
// once by an operator and used only to build a descriptor here. Projecting them
// outward would let a caller learn how to compose a launch the review never
// approved, so the published view is defined as its own type rather than by
// omitting fields at each call site — a new field on StaticProfile then cannot
// leak by default.
type PublishedProfile struct {
	ID                 string
	Harness            HarnessID
	AllowedShapes      []domain.TaskShape
	Availability       Availability
	AvailabilityReason AvailabilityReason
	Unattended         bool
	ConcurrencyLimit   int
}

// PublishedProfiles reports every configured profile with its posture, ordered
// by identity so two reads of an unchanged catalog agree.
//
// Unavailable profiles are included deliberately. "No profile accepts this
// shape" and "the profile that accepts it cannot run" are different answers, and
// a catalog that hid the second would leave a caller unable to tell them apart.
func (catalog *ProfileCatalog) PublishedProfiles() []PublishedProfile {
	if catalog == nil {
		return nil
	}
	published := make([]PublishedProfile, 0, len(catalog.profiles))
	for _, profile := range catalog.profiles {
		published = append(published, PublishedProfile{
			ID:                 profile.ID,
			Harness:            profile.Harness,
			AllowedShapes:      append([]domain.TaskShape(nil), profile.AllowedShapes...),
			Availability:       profile.Availability,
			AvailabilityReason: profile.AvailabilityReason,
			Unattended:         profile.Unattended,
			ConcurrencyLimit:   profile.ConcurrencyLimit,
		})
	}
	sort.Slice(published, func(first, second int) bool {
		return published[first].ID < published[second].ID
	})
	return published
}
