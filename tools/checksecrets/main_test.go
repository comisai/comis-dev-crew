package main

import (
	"bytes"
	"testing"
)

func TestScrubKnownNonSecretHistoryPreservesCredentialDetection(t *testing.T) {
	protocolMarker := "-----BEGIN " + "OPENSSH PRIVATE KEY-----"
	history := []byte(
		"+\t\t!bytes.HasPrefix(contents, []byte(\"" + protocolMarker + "\\n\")) ||\n" +
			"+\tprivateKey := \"" + protocolMarker + "\\nfixture\\n-----END OPENSSH PRIVATE KEY-----\\n\"\n" +
			"+real := `" + protocolMarker + "\ncredential-material\n-----END OPENSSH PRIVATE KEY-----`\n",
	)

	scrubbed := scrubKnownNonSecretHistory(history)
	if bytes.Contains(scrubbed, []byte("HasPrefix(contents")) || bytes.Contains(scrubbed, []byte("privateKey :=")) {
		t.Fatalf("known non-secret forge fragments remain: %q", scrubbed)
	}
	if !signatures[0].pattern.Match(scrubbed) {
		t.Fatal("a real private-key signature was scrubbed")
	}
}
