package localapi

import (
	"context"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func newProfileClient(t *testing.T, queries *apiQueries) *Client {
	t.Helper()
	socketPath, stop := startAPIServer(t, queries, CallerOperatorCLI, time.Now)
	t.Cleanup(stop)
	client, err := NewClient(socketPath, time.Second)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	return client
}

// The envelope's state version is the connection's read-after-write token, and
// the client checks it against the one the projection itself carries. A catalog
// read that reported no state version of its own would be rejected in transit
// with a mismatch — the read would be unreachable over the local API even though
// both handler and query were correct in isolation.
func TestLocalAPI_WhenTheCatalogIsRead_ItsStateVersionSurvivesTheRoundTrip(t *testing.T) {
	queries := &apiQueries{profiles: application.WorkerProfileList{
		SchemaVersion: 1,
		CapturedAtMs:  1_800_000_000_000,
		StateVersion:  47,
		Profiles: []application.WorkerProfileSummary{{
			ProfileID:     "profile_claude",
			Harness:       "claude-code",
			AllowedShapes: []domain.TaskShape{domain.ShapeShip},
			Availability:  "available",
			Unattended:    true,
		}},
	}}
	client := newProfileClient(t, queries)

	profiles, err := client.ListWorkerProfiles(context.Background(), "read-0001")
	if err != nil {
		t.Fatalf("ListWorkerProfiles() error = %v", err)
	}
	if profiles.StateVersion != 47 {
		t.Errorf("state version = %d, want 47", profiles.StateVersion)
	}
	if len(profiles.Profiles) != 1 || profiles.Profiles[0].ProfileID != "profile_claude" {
		t.Fatalf("catalog = %+v", profiles.Profiles)
	}
	if !profiles.Profiles[0].Unattended {
		t.Error("posture must survive the round trip")
	}
}

// A deployment that configured no profile is a real, answerable state: nothing
// can run here. Reporting it as a transport failure would send an operator
// hunting a broken connection instead of an empty catalog.
func TestLocalAPI_WhenNoProfileIsConfigured_ReportsAnEmptyCatalogNotAFailure(t *testing.T) {
	queries := &apiQueries{profiles: application.WorkerProfileList{
		SchemaVersion: 1, StateVersion: 3, Profiles: []application.WorkerProfileSummary{},
	}}
	client := newProfileClient(t, queries)

	profiles, err := client.ListWorkerProfiles(context.Background(), "read-0002")
	if err != nil {
		t.Fatalf("ListWorkerProfiles() error = %v", err)
	}
	if len(profiles.Profiles) != 0 {
		t.Errorf("profiles = %+v, want empty", profiles.Profiles)
	}
}

// The catalog is a read, so it belongs to the method set the transport admits
// without a mutation's evidence. A method missing from that set is refused
// before any handler runs.
func TestLocalAPI_TheCatalogReadIsAKnownMethod(t *testing.T) {
	if !MethodWorkerProfiles.valid() {
		t.Fatal("the worker-profile catalog must be a known local API method")
	}
}
