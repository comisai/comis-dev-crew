package git

import (
	"strings"
	"testing"
)

// The change summary arrives as raw bytes from Git, so its decoder is the layer
// that decides what reaches an operator's terminal. Each malformed shape is a
// refusal rather than a best-effort row: a half-decoded change record would
// attribute real work to the wrong path or the wrong extent.
func TestParseNumstat_RefusesEveryMalformedRecord(t *testing.T) {
	for name, output := range map[string]string{
		"no separator":        "12 3 path.go\x00",
		"one separator":       "12\t3 path.go\x00",
		"added not a number":  "x\t3\tpath.go\x00",
		"deleted not number":  "12\ty\tpath.go\x00",
		"negative added":      "-2\t3\tpath.go\x00",
		"negative deleted":    "12\t-3\tpath.go\x00",
		"incomplete rename":   "1\t1\t\x00only-one-path\x00",
		"rename with no tail": "1\t1\t\x00",
		"empty path":          "1\t1\t\x00\x00\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNumstat([]byte(output)); err == nil {
				t.Fatalf("parseNumstat(%q) error = nil, want a refusal", output)
			}
		})
	}
}

// A path Git reports verbatim can carry anything a worker chose to name a file.
// Refusing rather than escaping keeps a control sequence from reaching whatever
// renders the row, and an anomalous name is worth stopping on.
func TestValidateDiffPath_RefusesUnsafeAndUnboundedPaths(t *testing.T) {
	for name, path := range map[string]string{
		"empty":            "",
		"escape sequence":  "a\x1b[31mred.go",
		"newline":          "a\nb.go",
		"carriage return":  "a\rb.go",
		"delete character": "a\x7fb.go",
		"invalid encoding": "a\xffb.go",
		"unbounded":        strings.Repeat("a", 1025),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateDiffPath(path); err == nil {
				t.Fatalf("validateDiffPath(%q) error = nil, want a refusal", path)
			}
		})
	}
	if err := validateDiffPath("internal/thing.go"); err != nil {
		t.Errorf("validateDiffPath(ordinary path) error = %v", err)
	}
}

// An unsafe path is refused wherever it appears, including either half of a
// rename, so a rename cannot smuggle one past the check its ordinary sibling
// would have failed.
func TestParseNumstat_RefusesAnUnsafePathInEitherHalfOfARename(t *testing.T) {
	for name, output := range map[string]string{
		"unsafe current":  "1\t1\tbad\x1b[31m.go\x00",
		"unsafe previous": "1\t1\t\x00old\x1b[31m.go\x00new.go\x00",
		"unsafe renamed":  "1\t1\t\x00old.go\x00new\x1b[31m.go\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseNumstat([]byte(output)); err == nil {
				t.Fatalf("parseNumstat(%q) error = nil, want a refusal", output)
			}
		})
	}
}

// A binary change reports no counts. Recording zeros would present it as a file
// that was touched and changed nothing.
func TestParseNumstat_ReadsBinaryRenamesAndOrdinaryRecords(t *testing.T) {
	changes, err := parseNumstat([]byte("12\t3\tinternal/thing.go\x00-\t-\tassets/logo.png\x004\t0\t\x00old.go\x00new.go\x00"))
	if err != nil {
		t.Fatalf("parseNumstat() error = %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %#v", changes)
	}
	if changes[0].Path != "internal/thing.go" || changes[0].Added != 12 || changes[0].Deleted != 3 {
		t.Errorf("ordinary change = %#v", changes[0])
	}
	if !changes[1].Binary || changes[1].Added != 0 || changes[1].Deleted != 0 {
		t.Errorf("binary change = %#v", changes[1])
	}
	if changes[2].PreviousPath != "old.go" || changes[2].Path != "new.go" || changes[2].Added != 4 {
		t.Errorf("rename = %#v", changes[2])
	}
	if empty, err := parseNumstat(nil); err != nil || len(empty) != 0 {
		t.Errorf("parseNumstat(empty) = %#v, %v", empty, err)
	}
}

func TestSummarizeDiff_CountsBinaryFilesApartFromLines(t *testing.T) {
	totals := summarizeDiff([]CandidateFileChange{
		{Path: "a.go", Added: 3, Deleted: 1},
		{Path: "b.png", Binary: true},
	})
	if totals.Files != 2 || totals.Added != 3 || totals.Deleted != 1 || totals.BinaryFiles != 1 {
		t.Fatalf("totals = %#v", totals)
	}
}
