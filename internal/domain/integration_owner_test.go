package domain

import "testing"

func integrationInitiative() DevelopmentInitiative {
	initiative := initiativeFixtureInPackage()
	initiative.IntegrationOwnerTask = "task-integration"
	return initiative
}

func TestOnlyTheIntegrationOwnerMayApplyCandidates(t *testing.T) {
	initiative := integrationInitiative()
	if err := initiative.AuthorizeIntegrationWrite("task-integration"); err != nil {
		t.Fatalf("the recorded owner was refused: %v", err)
	}
	// A component task publishes candidates; it never receives the integration
	// lease. Two writers applying to one target is how a merge race becomes an
	// unattributable conflict.
	for _, other := range []string{"task-backend", "task-frontend"} {
		if err := initiative.AuthorizeIntegrationWrite(other); err == nil {
			t.Fatalf("%s was allowed to integrate", other)
		}
	}
}

func TestIntegrationWriteIsRefusedWhenNoOwnerIsRecorded(t *testing.T) {
	initiative := integrationInitiative()
	initiative.IntegrationOwnerTask = ""
	// No owner means no single writer, so there is nothing to be single about.
	// Allowing any member here would silently make every member a writer.
	if err := initiative.AuthorizeIntegrationWrite("task-integration"); err == nil {
		t.Fatal("integration was allowed with no recorded owner")
	}
}

func TestIntegrationOwnerMustHoldItsOwnWorktree(t *testing.T) {
	initiative := integrationInitiative()
	err := initiative.AuthorizeIntegrationWorktree("task-integration", map[string]string{
		"task-integration": "/approved/worktrees/integration",
		"task-backend":     "/approved/worktrees/backend",
	})
	if err != nil {
		t.Fatalf("distinct integration worktree refused: %v", err)
	}
	// Sharing a worktree with a component would let a component's uncommitted
	// work appear inside the integration result without ever being a candidate.
	err = initiative.AuthorizeIntegrationWorktree("task-integration", map[string]string{
		"task-integration": "/approved/worktrees/backend",
		"task-backend":     "/approved/worktrees/backend",
	})
	if err == nil {
		t.Fatal("shared integration worktree accepted")
	}
}

func TestIntegrationOwnerWithoutAWorktreeIsRefused(t *testing.T) {
	initiative := integrationInitiative()
	if err := initiative.AuthorizeIntegrationWorktree("task-integration", map[string]string{
		"task-backend": "/approved/worktrees/backend",
	}); err == nil {
		t.Fatal("integration owner with no worktree accepted")
	}
}

// initiativeFixtureInPackage mirrors the external fixture for in-package tests.
func initiativeFixtureInPackage() DevelopmentInitiative {
	return DevelopmentInitiative{
		SchemaVersion:     1,
		Handle:            "initiative-alpha",
		ManagedRunGroupID: "managed-run-group_a",
		TitleRef:          "title-alpha",
		State:             InitiativePreparing,
		BaseRevisionSet: []InitiativeBaseRevision{
			{RepositoryID: "repo-primary", Revision: "0123456789abcdef0123456789abcdef01234567"},
		},
		Components: []InitiativeComponent{
			{ComponentHandle: "component-backend", RepositoryID: "repo-primary", TaskHandles: []string{"task-backend"}},
			{ComponentHandle: "component-frontend", RepositoryID: "repo-primary", TaskHandles: []string{"task-frontend"}},
			{ComponentHandle: "component-integration", RepositoryID: "repo-primary", TaskHandles: []string{"task-integration"}},
		},
		IntegrationPolicyID: "integration-default",
		StateVersion:        1,
	}
}
