package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeOversizeThenFailScript writes a child that produces more than the output
// bound and then exits non-zero, which is what an overflowing read looks like
// from the outside: the reader stops, the child's pipe closes under it, and the
// child dies rather than exiting cleanly.
func writeOversizeThenFailScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "oversize")
	script := "#!/bin/sh\n" +
		"index=0\n" +
		"while [ $index -lt 400 ]; do\n" +
		"  printf '%s\\n' 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'\n" +
		"  index=$((index+1))\n" +
		"done\n" +
		"exit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

// A read that outgrew its bound reports the bound, whatever exit status the child
// reached. The child's status is a consequence of the reader stopping, so letting
// it win makes an oversize read look like a failed command on one platform and a
// truncated listing on another.
func TestExecuteGitReportsAnOversizeReadRegardlessOfChildExitStatus(t *testing.T) {
	_, _, err := executeGit(context.Background(), writeOversizeThenFailScript(t))
	if !errors.Is(err, errGitOutputTooLarge) {
		t.Fatalf("executeGit(oversize child that exits non-zero) error = %v, want errGitOutputTooLarge", err)
	}
}

// A failing child names the status it exited with. The status is a number rather
// than child output, so it stays content-free while still saying which failure
// class an operator is looking at.
func TestRunGitBytesNamesTheChildExitStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fail")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 3\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := runGitBytes(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "3") {
		t.Fatalf("runGitBytes(child exiting 3) error = %v, want the exit status named", err)
	}
}
