package git

import (
	"context"
	"errors"
	"testing"
)

func TestGitEntryPointsRejectMissingAuthority(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewRegistry(canceled, RegistryConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("NewRegistry(canceled) error = %v", err)
	}
	if _, err := NewRegistry(nil, RegistryConfig{}); err == nil {
		t.Fatal("NewRegistry(nil context) succeeded")
	}
	var registry *Registry
	if _, err := registry.InspectCandidate(context.Background(), CandidateSnapshotRequest{}); err == nil {
		t.Fatal("InspectCandidate(nil registry) succeeded")
	}
	if _, err := registry.PrepareFixtureCandidate(context.Background(), FixtureCandidateRequest{}); err == nil {
		t.Fatal("PrepareFixtureCandidate(nil registry) succeeded")
	}
	if _, err := registry.PrepareWorktree(context.Background(), PrepareWorktreeRequest{}); err == nil {
		t.Fatal("PrepareWorktree(nil registry) succeeded")
	}
	if err := registry.CleanupWorktree(context.Background(), CleanupWorktreeRequest{}); err == nil {
		t.Fatal("CleanupWorktree(nil registry) succeeded")
	}
	if err := registry.RemoveDeliveredWorktree(context.Background(), DeliveredWorktreeCleanupRequest{}); err == nil {
		t.Fatal("RemoveDeliveredWorktree(nil registry) succeeded")
	}
}
