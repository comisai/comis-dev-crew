package git_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	devgit "github.com/comisai/comis-dev-crew/internal/git"
)

func TestRegistry_CandidateInspectionIgnoresRacingDynamicFilterProcess(t *testing.T) {
	fixture := newIntegrationFixture(t)
	commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath, "component.txt", "component\n")
	wrapper, marker, armed := writeRacingCandidateInspectionDriver(t, fixture, "filter")
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)

	snapshot, err := registry.InspectCandidate(context.Background(), devgit.CandidateSnapshotRequest{
		TaskHandle: fixture.candidate.TaskHandle, RepositoryID: fixture.repository.repositoryID,
		WorktreePath: fixture.candidate.CanonicalPath,
	})
	if _, statErr := os.Lstat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("candidate filter process executed: %v", statErr)
	}
	if _, statErr := os.Lstat(armed); statErr != nil {
		t.Fatalf("candidate filter race was not armed: %v", statErr)
	}
	if err != nil || snapshot.HeadRevision == "" {
		t.Fatalf("InspectCandidate(dynamic filter race) = %#v, %v", snapshot, err)
	}
}

func TestRegistry_CandidateDiffIgnoresDynamicTextConversionDriver(t *testing.T) {
	fixture := newIntegrationFixture(t)
	attributes := filepath.Join(fixture.candidate.CanonicalPath, ".gitattributes")
	component := filepath.Join(fixture.candidate.CanonicalPath, "component.bin")
	if err := os.WriteFile(attributes, []byte("component.bin diff=reviewrace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(component, []byte("component\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
		"add", "--", ".gitattributes", "component.bin")
	runGit(t, fixture.repository.gitExecutable, "--no-optional-locks", "-C", fixture.candidate.CanonicalPath,
		"-c", fixtureAuthorName, "-c", fixtureAuthorEmail, "commit", "-m", "fixture attributed change")
	wrapper, marker, armed := writeRacingCandidateInspectionDriver(t, fixture, "diff")
	registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)

	diff, err := registry.InspectCandidateDiff(context.Background(), devgit.CandidateDiffRequest{
		TaskHandle: fixture.candidate.TaskHandle, RepositoryID: fixture.repository.repositoryID,
		WorktreePath: fixture.candidate.CanonicalPath, BaseRevision: fixture.base,
	})
	if _, statErr := os.Lstat(marker); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("candidate text conversion driver executed: %v", statErr)
	}
	if _, statErr := os.Lstat(armed); statErr != nil {
		t.Fatalf("candidate diff race was not armed: %v", statErr)
	}
	if err != nil || len(diff.Committed) == 0 {
		t.Fatalf("InspectCandidateDiff(dynamic text conversion) = %#v, %v", diff, err)
	}
}

func writeRacingCandidateInspectionDriver(
	t *testing.T,
	fixture integrationFixture,
	mode string,
) (string, string, string) {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-candidate-inspection-race")
	driver := filepath.Join(root, "candidate-driver")
	marker := filepath.Join(root, "candidate-driver-ran")
	armed := filepath.Join(root, "candidate-driver-armed")
	common := integrationGitOutput(t, fixture, fixture.candidate.CanonicalPath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	driverScript := fmt.Sprintf("#!/bin/sh\n: > %s\nexit 1\n", quote(marker))
	if err := os.WriteFile(driver, []byte(driverScript), 0o700); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
real=%s
candidate=%s
common=%s
driver=%s
armed=%s
mode=%s
status=false
diff=false
lsfiles=false
lstree=false
for argument in "$@"; do
  if [ "$argument" = status ]; then status=true; fi
  if [ "$argument" = diff ]; then diff=true; fi
  if [ "$argument" = ls-files ]; then lsfiles=true; fi
  if [ "$argument" = ls-tree ]; then lstree=true; fi
done
fire=false
if [ "$mode" = filter ] && { [ "$status" = true ] || [ "$lsfiles" = true ]; }; then fire=true; fi
if [ "$mode" = diff ] && { [ "$diff" = true ] || [ "$lstree" = true ]; }; then fire=true; fi
if [ "$fire" = true ] && [ ! -f "$armed" ]; then
  : > "$armed"
  if [ "$mode" = filter ]; then
    "$real" --no-optional-locks -C "$candidate" config --local filter.reviewrace.process "$driver" || exit $?
    "$real" --no-optional-locks -C "$candidate" config --local filter.reviewrace.clean "$driver" || exit $?
    "$real" --no-optional-locks -C "$candidate" config --local filter.reviewrace.smudge "$driver" || exit $?
    printf 'component.txt filter=reviewrace\n' > "$common/info/attributes" || exit $?
  else
    "$real" --no-optional-locks -C "$candidate" config --local diff.reviewrace.textconv "$driver" || exit $?
    "$real" --no-optional-locks -C "$candidate" config --local diff.reviewrace.command "$driver" || exit $?
    "$real" --no-optional-locks -C "$candidate" config --local diff.external "$driver" || exit $?
  fi
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(fixture.candidate.CanonicalPath), quote(common), quote(driver),
		quote(armed), quote(mode))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper, marker, armed
}
