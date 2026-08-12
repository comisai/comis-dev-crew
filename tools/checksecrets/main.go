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

// exception excuses one reviewed line that carries a credential signature for a
// legitimate reason. Each pattern must be narrow enough that a real credential
// on the same line still fails: match the surrounding code, never the signature
// alone.
type exception struct {
	reason  string
	pattern *regexp.Regexp
}

// reviewedExceptions is the single, centralized, shrink-only list of excused
// lines. Add an entry only after reading the line and confirming it holds no
// usable credential, and remove the entry when the code that needed it changes.
// Never add one to silence a finding that has not been read.
var reviewedExceptions = []exception{
	{
		reason:  "the forge deploy-key validator recognizes a key by its armor prefix; the literal is the check itself and carries no key material",
		pattern: regexp.MustCompile(`bytes\.HasPrefix\(.*` + opensshArmor),
	},
	{
		reason:  "forge deploy-key tests build an armored fixture whose entire key body is the word fixture",
		pattern: regexp.MustCompile(opensshArmor + `\\nfixture\\n`),
	},
}

// opensshArmor is assembled from parts on purpose. Written as one literal it
// would match the private-key signature above, and the Git history scan reads
// this file's diff without the self-skip that spares the file itself.
const opensshArmor = "-----BEGIN OPENSSH PRIVATE" + " KEY-----"

// finding locates a credential signature without reproducing the matched text.
// Diagnostics stay content-free: a scanner that echoes what it found would
// publish the very secret it exists to catch.
type finding struct {
	source string
	line   int
	name   string
}

func main() {
	filesOutput, err := exec.Command("git", "ls-files", "--cached", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		fatal("list repository files", err)
	}
	var findings []finding
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
		findings = append(findings, scan(name, contents)...)
	}
	history, err := exec.Command("git", "log", "-p", "--all", "--no-ext-diff", "--format=").Output()
	if err != nil {
		fatal("scan Git history", err)
	}
	findings = append(findings, scan("Git history", history)...)

	if len(findings) > 0 {
		for _, item := range findings {
			fmt.Fprintf(os.Stderr, "%s:%d: detected %s signature\n", item.source, item.line, item.name)
		}
		fmt.Fprintf(os.Stderr, "%d credential signature(s) found\n", len(findings))
		os.Exit(1)
	}
	fmt.Println("secret scan found no credential signatures")
	// Disclose the exceptions on every clean run. A silent allowlist is how a
	// scanner goes blind without anyone noticing it happened.
	for _, item := range reviewedExceptions {
		fmt.Printf("reviewed exception: %s\n", item.reason)
	}
}

// scan reports every unexcused credential signature in contents. It examines
// one line at a time so a reviewed exception excuses only its own line rather
// than blinding the scanner to the rest of the file or history.
func scan(source string, contents []byte) []finding {
	if strings.HasSuffix(source, "tools/checksecrets/main.go") {
		return nil
	}
	var findings []finding
	for index, rawLine := range bytes.Split(contents, []byte{'\n'}) {
		line := string(rawLine)
		for _, item := range signatures {
			if !item.pattern.MatchString(line) {
				continue
			}
			if excused(line) {
				continue
			}
			findings = append(findings, finding{source: source, line: index + 1, name: item.name})
		}
	}
	return findings
}

func excused(line string) bool {
	for _, item := range reviewedExceptions {
		if item.pattern.MatchString(line) {
			return true
		}
	}
	return false
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
