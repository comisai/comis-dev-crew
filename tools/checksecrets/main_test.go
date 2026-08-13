package main

import "testing"

// rsaArmor is assembled from parts for the same reason as opensshArmor in
// main.go: a single literal would plant a real matching signature in this file,
// which the scanner reads like any other tracked source.
const rsaArmor = "-----BEGIN RSA PRIVATE" + " KEY-----"

func TestScan_DetectsAnArmoredPrivateKey(t *testing.T) {
	contents := opensshArmor + "\nb3BlbnNzaC1rZXktdjEAAAAABG5vbmUAAAAEbm9uZQAAAAA\n"

	findings := scan("deploy.pem", []byte(contents))

	if len(findings) != 1 {
		t.Fatalf("scan() findings = %d, want 1", len(findings))
	}
	if findings[0].name != "private key" || findings[0].source != "deploy.pem" || findings[0].line != 1 {
		t.Fatalf("scan() = %#v", findings[0])
	}
}

func TestScan_ExcusesTheReviewedDeployKeyValidator(t *testing.T) {
	line := `	!bytes.HasPrefix(contents, []byte("` + opensshArmor + `\n")) ||`

	if findings := scan("internal/forge/git_pusher.go", []byte(line)); len(findings) != 0 {
		t.Fatalf("reviewed validator line was reported: %#v", findings)
	}
}

func TestScan_ExcusesTheReviewedFixtureBody(t *testing.T) {
	line := "	privateKey := \"" + opensshArmor + `\nfixture\n-----END OPENSSH PRIVATE KEY-----\n"`

	if findings := scan("internal/forge/git_pusher_test.go", []byte(line)); len(findings) != 0 {
		t.Fatalf("reviewed fixture line was reported: %#v", findings)
	}
}

// An exception must excuse its own reviewed shape and nothing wider. A
// different armor on the same surrounding code is still a finding.
func TestScan_DoesNotExcuseADifferentArmorOnTheSameShape(t *testing.T) {
	line := `	!bytes.HasPrefix(contents, []byte("` + rsaArmor + `\n")) ||`

	if findings := scan("internal/forge/git_pusher.go", []byte(line)); len(findings) != 1 {
		t.Fatalf("scan() findings = %d, want 1", len(findings))
	}
}

// An excused line excuses only itself. Real key material elsewhere in the same
// content must still fail.
func TestScan_ExcusedLineDoesNotBlindTheRestOfTheContent(t *testing.T) {
	contents := `	!bytes.HasPrefix(contents, []byte("` + opensshArmor + `\n")) ||` + "\n" +
		"some other line\n" +
		opensshArmor + "\n"

	findings := scan("mixed.go", []byte(contents))

	if len(findings) != 1 {
		t.Fatalf("scan() findings = %d, want 1", len(findings))
	}
	if findings[0].line != 3 {
		t.Fatalf("scan() line = %d, want 3", findings[0].line)
	}
}

func TestScan_DetectsTheOtherCredentialClasses(t *testing.T) {
	for _, test := range []struct {
		name     string
		contents string
		want     string
	}{
		{name: "aws", contents: "id = " + "AKIA" + "IOSFODNN7EXAMPLE", want: "AWS access key"},
		{name: "github", contents: "ghp_" + "0123456789abcdefghijklmnopqrstuvwxyz", want: "GitHub token"},
		{name: "slack", contents: "xoxb-" + "0123456789-0123456789-abcdefghijkl", want: "Slack token"},
		{name: "assigned", contents: "password = " + "abcdefghijklmnopqrstuvwxyz012345", want: "assigned high-entropy secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			findings := scan("config.txt", []byte(test.contents))
			if len(findings) != 1 {
				t.Fatalf("scan() findings = %d, want 1", len(findings))
			}
			if findings[0].name != test.want {
				t.Fatalf("scan() name = %q, want %q", findings[0].name, test.want)
			}
		})
	}
}

// The scanner declares the signatures it hunts for, so reading its own source
// would report every one of them.
func TestScan_SkipsItsOwnSource(t *testing.T) {
	contents := opensshArmor + "\n"

	if findings := scan("tools/checksecrets/main.go", []byte(contents)); len(findings) != 0 {
		t.Fatalf("scanner reported its own source: %#v", findings)
	}
}

func TestScan_ReportsCleanContentAsNoFindings(t *testing.T) {
	if findings := scan("README.md", []byte("# comis-dev-crew\n\nNothing secret here.\n")); len(findings) != 0 {
		t.Fatalf("clean content reported: %#v", findings)
	}
}
