package git

import "testing"

func TestCandidateContentChangePreservesFinalNewlineIdentity(t *testing.T) {
	for _, test := range []struct {
		name           string
		before, after  string
		added, deleted int
	}{
		{name: "remove final newline", before: "x\n", after: "x", added: 1, deleted: 1},
		{name: "add final newline", before: "x", after: "x\n", added: 1, deleted: 1},
		{name: "newline only", before: "\n", after: "", deleted: 1},
		{name: "empty to newline", before: "", after: "\n", added: 1},
		{name: "crlf to lf", before: "x\r\n", after: "x\n", added: 1, deleted: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			change := candidateContentChange("fixture.txt", "", []byte(test.before), []byte(test.after))
			if change.Added != test.added || change.Deleted != test.deleted || change.Binary {
				t.Fatalf("candidateContentChange() = %#v, want +%d/-%d", change, test.added, test.deleted)
			}
		})
	}
}
