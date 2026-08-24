package git_test

import (
	"context"
	"reflect"
	"testing"
)

func TestRegistry_ReportsRemoteTrackingRefsContainingExactHead(t *testing.T) {
	fixture := newIntegrationFixture(t)
	head := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "remote.txt", "preserved\n")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.repository.primary,
		"update-ref", "refs/remotes/fork/feature", head)

	refs, err := fixture.registry.ReachableRemoteRefs(context.Background(), fixture.repository.repositoryID, head)
	if err != nil {
		t.Fatalf("ReachableRemoteRefs() error = %v", err)
	}
	if want := []string{"fork/feature"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("ReachableRemoteRefs() = %#v, want %#v", refs, want)
	}
}
