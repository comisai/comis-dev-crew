package architecture_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchitecture_ProtocolPinningHasStableMakeCommands(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(contents)
	for _, target := range []string{"protocol-sync:", "protocol-check:"} {
		if !strings.Contains(makefile, target) {
			t.Errorf("Makefile is missing %s", target)
		}
	}
	if !strings.Contains(makefile, "COMIS_ROOT") || !strings.Contains(makefile, "COMIS_COMMIT") {
		t.Error("protocol sync must require an explicit source root and commit")
	}
}
