package git_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_RejectsUnsupportedTopologyBeforeSharedObjectPublication(t *testing.T) {
	for _, strategy := range []application.IntegrationStrategy{
		application.IntegrationMerge,
		application.IntegrationCherryPick,
		application.IntegrationRebase,
	} {
		t.Run(string(strategy), func(t *testing.T) {
			fixture := newIntegrationFixture(t)
			candidateHead := commitIntegrationFile(
				t, fixture, fixture.candidate.CanonicalPath, "fixture.txt", "candidate rewrite\n",
			)
			targetHead := commitIntegrationFile(
				t, fixture, fixture.target.CanonicalPath, "target-only.txt", "target only\n",
			)
			objects := filepath.Join(gitOutput(t, fixture.repository.gitExecutable,
				"--no-optional-locks", "-C", fixture.target.CanonicalPath,
				"rev-parse", "--path-format=absolute", "--git-common-dir"), "objects")
			before := snapshotObjectDatabase(t, objects)

			operationID := "prepublication-" + strings.ReplaceAll(string(strategy), "_", "-")
			request := fixture.request(operationID, strategy, candidateHead, targetHead)
			if _, err := fixture.registry.ApplyIntegrationCandidate(context.Background(), request); err == nil ||
				!errors.Is(err, application.ErrIntegrationMutationNotStarted) {
				t.Fatalf("ApplyIntegrationCandidate(unsupported topology) error = %v", err)
			}
			after := snapshotObjectDatabase(t, objects)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("shared object database changed before topology refusal: before=%d after=%d", len(before), len(after))
			}
			if head := integrationGitOutput(t, fixture, fixture.target.CanonicalPath, "rev-parse", "HEAD"); head != targetHead {
				t.Fatalf("target head = %q, want %q", head, targetHead)
			}
		})
	}
}

func snapshotObjectDatabase(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	snapshot := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		snapshot[relative] = sha256.Sum256(contents)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}
