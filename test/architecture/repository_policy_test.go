package architecture_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve repository-policy test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
}

func readRepositoryFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), name))
	if err != nil {
		t.Fatalf("read required repository file %s: %v", name, err)
	}
	return string(contents)
}

func TestRepositoryPolicy_AuthoritativeInstructionsRemainPresent(t *testing.T) {
	agents := readRepositoryFile(t, "AGENTS.md")
	requiredSections := []string{
		"## Architecture and authority",
		"## Go engineering",
		"## Security",
		"## Testing and verification",
		"## Commits and external actions",
	}
	for _, section := range requiredSections {
		if !strings.Contains(agents, section) {
			t.Errorf("AGENTS.md is missing required section %q", section)
		}
	}
	if !strings.Contains(agents, "Never add a `Co-Authored-By:` trailer") {
		t.Error("AGENTS.md must permanently prohibit Co-Authored-By trailers")
	}
}

func TestRepositoryPolicy_ClaudeNotesRemainSubordinate(t *testing.T) {
	claude := readRepositoryFile(t, "CLAUDE.md")
	required := "Read and follow `AGENTS.md` before making changes. `AGENTS.md` is the authoritative repository protocol and wins every conflict."
	if !strings.Contains(claude, required) {
		t.Fatalf("CLAUDE.md must retain the AGENTS.md precedence declaration")
	}
}

func TestRepositoryPolicy_ProtectedWorkflowSuppliesEveryLiveRecoveryRoot(t *testing.T) {
	workflow := readRepositoryFile(t, ".github/workflows/live.yml")
	for _, required := range []string{
		"backup_root:",
		"restore_root:",
		"DEVCREW_LIVE_BACKUP_ROOT: ${{ inputs.backup_root }}",
		"DEVCREW_LIVE_RESTORE_ROOT: ${{ inputs.restore_root }}",
		`test -n "${DEVCREW_LIVE_BACKUP_ROOT}"`,
		`test -n "${DEVCREW_LIVE_RESTORE_ROOT}"`,
		"fresh_install_root:",
		"upgrade_root:",
		"DEVCREW_LIVE_FRESH_INSTALL_ROOT: ${{ inputs.fresh_install_root }}",
		"DEVCREW_LIVE_UPGRADE_ROOT: ${{ inputs.upgrade_root }}",
		`test -n "${DEVCREW_LIVE_FRESH_INSTALL_ROOT}"`,
		`test -n "${DEVCREW_LIVE_UPGRADE_ROOT}"`,
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("protected live workflow is missing recovery contract %q", required)
		}
	}
}

// The companion owns the operator-installed liaison assets. Comis must not ship
// them, because its bundled-skill tree auto-seeds into every deployment, so this
// repository is where the skill and the named-agent policy templates have to be
// resolvable for an installer to copy.
func TestRepositoryPolicy_CompanionOwnsOperatorInstalledLiaisonAssets(t *testing.T) {
	for _, name := range []string{
		"skills/dev-crew/SKILL.md",
		"skills/dev-crew/references/task-shapes.md",
		"skills/dev-crew/references/delegation.md",
		"skills/dev-crew/references/decisions.md",
		"skills/dev-crew/references/delivery.md",
		"skills/dev-crew/references/recovery.md",
		"workspace-template/ROLE.md",
		"workspace-template/TOOLS.md",
		"workspace-template/HEARTBEAT.md",
	} {
		if _, err := os.Stat(filepath.Join(repositoryRoot(t), name)); err != nil {
			t.Errorf("operator-installed liaison asset %s must be present: %v", name, err)
		}
	}
}

// A skill recommends procedure. It can never be the enforcement layer for a
// credential, an approval, or a capability, so it must not present itself as one.
func TestRepositoryPolicy_LiaisonSkillGrantsNoAuthority(t *testing.T) {
	skill := readRepositoryFile(t, "skills/dev-crew/SKILL.md")
	if !strings.Contains(skill, "grants no capability") {
		t.Error("the liaison skill must state that it grants no capability")
	}
	for _, forbidden := range []string{
		"devcrew ", // no operator command lines for a model to assemble
		"sqlite",
		"credential",
		"secret://",
	} {
		if strings.Contains(strings.ToLower(skill), forbidden) &&
			!strings.Contains(strings.ToLower(skill), "never "+forbidden) &&
			!strings.Contains(strings.ToLower(skill), "`"+strings.TrimSpace(forbidden)+"`") {
			t.Errorf("the liaison skill must not present %q as an available surface", forbidden)
		}
	}
}
