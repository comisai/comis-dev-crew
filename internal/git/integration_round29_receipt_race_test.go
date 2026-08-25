package git_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func TestRegistry_FreshMaterializationRejectsRacingReceiptFamily(t *testing.T) {
	for _, strategy := range []application.IntegrationStrategy{
		application.IntegrationMerge,
		application.IntegrationCherryPick,
	} {
		for _, boundary := range []string{"before-index", "before-terminal"} {
			t.Run(string(strategy)+"/"+boundary, func(t *testing.T) {
				fixture := newIntegrationFixture(t)
				candidateHead := commitIntegrationFile(t, fixture, fixture.candidate.CanonicalPath,
					"component.txt", "component\n")
				targetHead := commitIntegrationFile(t, fixture, fixture.target.CanonicalPath,
					"target.txt", "target\n")
				identity := strings.ReplaceAll(string(strategy), "_", "-")
				request := fixture.request("integration-receipt-race-"+identity+"-"+boundary,
					strategy, candidateHead, targetHead)
				racingReceipt := integrationReceiptRefForTest("target", request)
				wrapper := writeFreshMaterializationReceiptRaceWrapper(
					t, fixture, boundary, racingReceipt, integrationReceiptRefForTest("applied", request),
				)
				registry := newIntegrationRegistryWithExecutable(t, fixture, wrapper)

				result, err := registry.ApplyIntegrationCandidate(context.Background(), request)
				if err == nil || result.Outcome == application.IntegrationApplied {
					t.Fatalf("ApplyIntegrationCandidate(%s receipt race) = %#v, %v", boundary, result, err)
				}
				if target, inspectErr := integrationGitOutputError(fixture.repository.gitExecutable,
					fixture.target.CanonicalPath, "symbolic-ref", "--no-recurse", racingReceipt); inspectErr != nil || target != "refs/heads/missing-racing-receipt" {
					t.Fatalf("racing receipt = %q, %v", target, inspectErr)
				}
			})
		}
	}
}

func writeFreshMaterializationReceiptRaceWrapper(
	t *testing.T,
	fixture integrationFixture,
	boundary string,
	racingReceipt string,
	appliedReceipt string,
) string {
	t.Helper()
	root := canonicalTempDir(t)
	wrapper := filepath.Join(root, "git-materialization-receipt-race")
	marker := filepath.Join(root, "receipt-race-fired")
	common := integrationGitOutput(t, fixture, fixture.target.CanonicalPath,
		"rev-parse", "--path-format=absolute", "--git-common-dir")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
	script := fmt.Sprintf(`#!/bin/sh
real=%s
common=%s
boundary=%s
racing=%s
applied=%s
marker=%s
read_tree=false
applied_update=false
for argument in "$@"; do
  if [ "$argument" = read-tree ]; then read_tree=true; fi
  if [ "$argument" = "$applied" ]; then applied_update=true; fi
done
fire=false
if [ "$boundary" = before-index ] && [ "$read_tree" = true ]; then fire=true; fi
if [ "$boundary" = before-terminal ] && [ "$applied_update" = true ]; then fire=true; fi
if [ "$fire" = true ] && [ ! -f "$marker" ]; then
  : > "$marker"
  "$real" --git-dir="$common" symbolic-ref "$racing" refs/heads/missing-racing-receipt || exit $?
fi
exec "$real" "$@"
`, quote(fixture.repository.gitExecutable), quote(common), quote(boundary), quote(racingReceipt),
		quote(appliedReceipt), quote(marker))
	if err := os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return wrapper
}
