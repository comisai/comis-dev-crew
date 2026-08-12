package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

type signature struct {
	name    string
	pattern *regexp.Regexp
}

var signatures = []signature{
	{name: "private key", pattern: regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`)},
	{name: "AWS access key", pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{name: "GitHub token", pattern: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{30,}`)},
	{name: "Slack token", pattern: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{20,}`)},
	{name: "assigned high-entropy secret", pattern: regexp.MustCompile(`(?i)(?:api[_-]?key|password|secret|token)\s*[:=]\s*["']?[A-Za-z0-9_./+=-]{24,}`)},
}

var knownNonSecretHistoryFragments = [][]byte{
	[]byte(`!bytes.HasPrefix(contents, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\n")) ||`),
	[]byte(`privateKey := "-----BEGIN OPENSSH PRIVATE KEY-----\nfixture\n-----END OPENSSH PRIVATE KEY-----\n"`),
}

func main() {
	filesOutput, err := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		fatal("list repository files", err)
	}
	failed := false
	for _, rawName := range bytes.Split(filesOutput, []byte{0}) {
		if len(rawName) == 0 {
			continue
		}
		name := string(rawName)
		contents, err := os.ReadFile(name)
		if err != nil {
			fatal("read "+name, err)
		}
		if len(contents) > 2<<20 || bytes.IndexByte(contents, 0) >= 0 {
			continue
		}
		failed = reportMatches(name, contents) || failed
	}
	history, err := exec.Command("git", "log", "-p", "--all", "--no-ext-diff", "--format=").Output()
	if err != nil {
		fatal("scan Git history", err)
	}
	history = scrubKnownNonSecretHistory(history)
	failed = reportMatches("Git history", history) || failed
	if failed {
		os.Exit(1)
	}
	fmt.Println("secret scan found no credential signatures")
}

func scrubKnownNonSecretHistory(contents []byte) []byte {
	scrubbed := contents
	for _, fragment := range knownNonSecretHistoryFragments {
		scrubbed = bytes.ReplaceAll(scrubbed, fragment, nil)
	}
	return scrubbed
}

func reportMatches(source string, contents []byte) bool {
	if strings.HasSuffix(source, "tools/checksecrets/main.go") {
		return false
	}
	failed := false
	for _, item := range signatures {
		if item.pattern.Match(contents) {
			fmt.Fprintf(os.Stderr, "%s: detected %s signature\n", source, item.name)
			failed = true
		}
	}
	return failed
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
