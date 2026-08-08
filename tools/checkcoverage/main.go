package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"strconv"
	"strings"
)

const modulePrefix = "github.com/comisai/comis-dev-crew/"

type policy struct {
	AggregateFloor   float64  `json:"aggregateFloor"`
	PackageFloor     float64  `json:"packageFloor"`
	CriticalFloor    float64  `json:"criticalFloor"`
	CriticalPackages []string `json:"criticalPackages"`
}

type block struct {
	statements int64
	count      int64
}

func main() {
	profilePath := flag.String("profile", "coverage.out", "Go coverage profile")
	flag.Parse()

	configuration := readPolicy()
	blocks := readProfile(*profilePath)
	packages := make(map[string][]block)
	for key, item := range blocks {
		file := strings.SplitN(key, ":", 2)[0]
		packages[path.Dir(file)] = append(packages[path.Dir(file)], item)
	}
	if len(packages) == 0 {
		fmt.Println("coverage: no hand-written internal packages yet")
		return
	}

	critical := make(map[string]bool)
	for _, packageName := range configuration.CriticalPackages {
		critical[packageName] = true
	}
	var totalStatements, coveredStatements int64
	failed := false
	for packageName, packageBlocks := range packages {
		covered, total := totals(packageBlocks)
		totalStatements += total
		coveredStatements += covered
		floor := configuration.PackageFloor
		if critical[packageName] {
			floor = configuration.CriticalFloor
		}
		percentage := percent(covered, total)
		fmt.Printf("coverage: %s %.1f%% (floor %.1f%%)\n", packageName, percentage, floor)
		if percentage < floor {
			failed = true
		}
	}
	aggregate := percent(coveredStatements, totalStatements)
	fmt.Printf("coverage: aggregate %.1f%% (floor %.1f%%)\n", aggregate, configuration.AggregateFloor)
	if aggregate < configuration.AggregateFloor {
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}

func readPolicy() policy {
	contents, err := os.ReadFile("tools/coverage-policy.json")
	if err != nil {
		fatal("read coverage policy", err)
	}
	var configuration policy
	if err := json.Unmarshal(contents, &configuration); err != nil {
		fatal("decode coverage policy", err)
	}
	return configuration
}

func readProfile(profilePath string) map[string]block {
	file, err := os.Open(profilePath)
	if err != nil {
		fatal("open coverage profile", err)
	}
	defer file.Close()
	blocks := make(map[string]block)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			fatal("parse coverage line", fmt.Errorf("unexpected line %q", line))
		}
		fileAndRange := fields[0]
		colon := strings.LastIndex(fileAndRange, ":")
		if colon < 0 {
			fatal("parse coverage path", fmt.Errorf("missing range in %q", line))
		}
		fileName := strings.TrimPrefix(fileAndRange[:colon], modulePrefix)
		if !strings.HasPrefix(fileName, "internal/") || generated(fileName) {
			continue
		}
		statements, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			fatal("parse statement count", err)
		}
		count, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			fatal("parse coverage count", err)
		}
		key := fileName + fileAndRange[colon:]
		item := block{statements: statements, count: count}
		if current, ok := blocks[key]; !ok || item.count > current.count {
			blocks[key] = item
		}
	}
	if err := scanner.Err(); err != nil {
		fatal("read coverage profile", err)
	}
	return blocks
}

func generated(fileName string) bool {
	contents, err := os.ReadFile(fileName)
	if err != nil {
		fatal("read covered source", err)
	}
	return strings.Contains(string(contents[:min(len(contents), 256)]), "Code generated")
}

func totals(blocks []block) (int64, int64) {
	var covered, total int64
	for _, item := range blocks {
		total += item.statements
		if item.count > 0 {
			covered += item.statements
		}
	}
	return covered, total
}

func percent(covered, total int64) float64 {
	if total == 0 {
		return 100
	}
	return float64(covered) * 100 / float64(total)
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
