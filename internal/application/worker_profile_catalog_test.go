package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func catalogQueries(t *testing.T, repository *queryRepository, catalog WorkerProfileCatalog) *Queries {
	t.Helper()
	queries, err := NewQueries(QueryConfig{
		Repository: repository, WorkerProfiles: catalog,
		Clock: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatalf("new queries: %v", err)
	}
	return queries
}

// A liaison that cannot start work must be able to tell three answers apart: no
// profile handles this shape, the profile exists but its harness is unavailable,
// and it is usable but cannot prove a turn settled. A preparation failure
// collapses all three into "it did not start", so the catalog reports posture
// per profile rather than leaving the caller to infer it.
func TestQueries_WhenProfilesAreConfigured_ReportsIdentityAndPostureNotJustNames(t *testing.T) {
	repository := &queryRepository{stateVersion: 12}
	queries := catalogQueries(t, repository, func() []WorkerProfileSummary {
		return []WorkerProfileSummary{
			{
				ProfileID: "profile_ship", Harness: "claude-code",
				AllowedShapes: []domain.TaskShape{domain.ShapeShip},
				Availability:  "available", Unattended: true, ConcurrencyLimit: 2,
			},
			{
				ProfileID: "profile_scout", Harness: "codex",
				AllowedShapes: []domain.TaskShape{domain.ShapeScout},
				Availability:  "unavailable", AvailabilityReason: "harness_not_installed",
			},
		}
	})

	catalog, err := queries.ListWorkerProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListWorkerProfiles() error = %v", err)
	}
	if len(catalog.Profiles) != 2 {
		t.Fatalf("profiles = %+v", catalog.Profiles)
	}
	if catalog.Profiles[1].Availability != "unavailable" ||
		catalog.Profiles[1].AvailabilityReason != "harness_not_installed" {
		t.Errorf("an unavailable profile must name why, got %+v", catalog.Profiles[1])
	}
	if catalog.Profiles[1].Unattended {
		t.Error("posture must be reported per profile, not inherited")
	}
	if catalog.StateVersion != 12 || catalog.SchemaVersion != 1 {
		t.Errorf("catalog = %+v, want state version 12 and schema 1", catalog)
	}
	if catalog.CapturedAtMs != time.Unix(1_800_000_000, 0).UTC().UnixMilli() {
		t.Errorf("captured at = %d, want the injected clock", catalog.CapturedAtMs)
	}
}

// "This deployment configured nothing that can run" is the answer, not a fault.
// Reporting it as an error would send an operator debugging the read instead of
// the configuration.
func TestQueries_WhenNoCatalogIsConfigured_ReportsAnEmptyCatalog(t *testing.T) {
	repository := &queryRepository{stateVersion: 4}
	queries := catalogQueries(t, repository, nil)

	catalog, err := queries.ListWorkerProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListWorkerProfiles() error = %v", err)
	}
	if catalog.Profiles == nil || len(catalog.Profiles) != 0 {
		t.Errorf("profiles = %+v, want an empty, non-nil catalog", catalog.Profiles)
	}
	if catalog.StateVersion != 4 {
		t.Errorf("state version = %d, want 4", catalog.StateVersion)
	}
}

// The state version is the connection's read-after-write token. A catalog that
// invented one when the store could not be read would let a caller believe it
// had observed state it never saw.
func TestQueries_WhenTheStateVersionCannotBeRead_RefusesRatherThanInventOne(t *testing.T) {
	repository := &queryRepository{stateVersionErr: errors.New("store unavailable")}
	queries := catalogQueries(t, repository, func() []WorkerProfileSummary { return nil })

	if _, err := queries.ListWorkerProfiles(context.Background()); err == nil {
		t.Fatal("expected the catalog read to refuse when state is unreadable")
	}
}

func TestQueries_WhenTheContextIsAbsentOrDone_RefusesTheCatalogRead(t *testing.T) {
	queries := catalogQueries(t, &queryRepository{stateVersion: 1}, nil)

	if _, err := queries.ListWorkerProfiles(nilCatalogContext()); err == nil {
		t.Error("ListWorkerProfiles(nil) error = nil")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := queries.ListWorkerProfiles(cancelled); !errors.Is(err, context.Canceled) {
		t.Errorf("ListWorkerProfiles(cancelled) error = %v", err)
	}
}

// Returned through a function so the nil is not a literal argument at the call
// site, matching how this package's own tests exercise the guard.
func nilCatalogContext() context.Context { return nil }
