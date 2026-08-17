package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var markdownLink = regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)

func main() {
	var problems []string
	err := filepath.WalkDir(".", func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "coverage" || entry.Name() == "dist" || entry.Name() == "bin") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".md" {
			return nil
		}
		fileProblems, err := checkMarkdown(path)
		if err != nil {
			return err
		}
		problems = append(problems, fileProblems...)
		return nil
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "documentation check failed: %v\n", err)
		os.Exit(1)
	}
	if len(problems) > 0 {
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, problem)
		}
		os.Exit(1)
	}
	fmt.Println("documentation structure and local links are valid")
}

func checkMarkdown(path string) ([]string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var problems []string
	firstContent := ""
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	lineNumber := 0
	// Installed assets carry a mandated machine-parsed preamble ahead of their
	// prose: a skill manifest opens with YAML frontmatter, and a workspace policy
	// template opens with the marker that identifies it as unedited. The H1 rule
	// applies to the prose that follows, so skip the preamble rather than the file.
	inFrontMatter := false
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if firstContent == "" {
			switch {
			case inFrontMatter:
				if trimmed == "---" {
					inFrontMatter = false
				}
				trimmed = ""
			case lineNumber == 1 && trimmed == "---":
				inFrontMatter = true
				trimmed = ""
			case strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->"):
				trimmed = ""
			}
		}
		if firstContent == "" && trimmed != "" {
			firstContent = line
		}
		if strings.TrimRight(line, " \t") != line {
			problems = append(problems, fmt.Sprintf("%s:%d: trailing whitespace", path, lineNumber))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(firstContent, "# ") {
		problems = append(problems, fmt.Sprintf("%s: first content line must be one H1 heading", path))
	}
	for _, match := range markdownLink.FindAllStringSubmatch(string(contents), -1) {
		target := strings.Trim(match[1], "<>")
		if target == "" || strings.HasPrefix(target, "#") || strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
			continue
		}
		target = strings.SplitN(target, "#", 2)[0]
		decoded, err := url.PathUnescape(target)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: invalid local link %q", path, target))
			continue
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(path), filepath.FromSlash(decoded)))
		if _, err := os.Stat(resolved); err != nil {
			problems = append(problems, fmt.Sprintf("%s: broken local link %q", path, target))
		}
	}
	return problems, nil
}
