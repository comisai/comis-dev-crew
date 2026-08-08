package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type module struct {
	Path     string
	Version  string
	Sum      string
	GoModSum string
	Dir      string
	GoMod    string
	Main     bool
	Replace  *module
}

type policy struct {
	AllowedClasses []string `json:"allowedClasses"`
}

func main() {
	configuration := readPolicy()
	allowed := make(map[string]bool)
	for _, licenseClass := range configuration.AllowedClasses {
		allowed[licenseClass] = true
	}
	output, err := exec.Command("go", "list", "-m", "-json", "all").Output()
	if err != nil {
		fatal("list modules", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(output))
	checked := 0
	for decoder.More() {
		var item module
		if err := decoder.Decode(&item); err != nil {
			fatal("decode module list", err)
		}
		if item.Main {
			continue
		}
		resolved := item
		if item.Replace != nil {
			resolved = *item.Replace
		}
		if resolved.Dir == "" && resolved.GoMod == "" {
			fmt.Printf("license: %s %s graph-only (not in the reachable package closure)\n", item.Path, item.Version)
			continue
		}
		if resolved.Version != "" && resolved.GoModSum == "" {
			fatal(item.Path, fmt.Errorf("module definition has no checksum provenance"))
		}
		if resolved.Sum == "" || resolved.Dir == "" {
			fmt.Printf("license: %s %s graph-only (go.mod checksum verified)\n", item.Path, item.Version)
			continue
		}
		licenseClass, licenseFile, err := classifyLicense(resolved.Dir)
		if err != nil {
			fatal(item.Path, err)
		}
		if !allowed[licenseClass] {
			fatal(item.Path, fmt.Errorf("license %s in %s is not allowed", licenseClass, licenseFile))
		}
		fmt.Printf("license: %s %s %s\n", item.Path, item.Version, licenseClass)
		checked++
	}
	fmt.Printf("license: verified %d external modules\n", checked)
}

func readPolicy() policy {
	contents, err := os.ReadFile("tools/licenses.json")
	if err != nil {
		fatal("read license policy", err)
	}
	var configuration policy
	if err := json.Unmarshal(contents, &configuration); err != nil {
		fatal("decode license policy", err)
	}
	return configuration
}

func classifyLicense(directory string) (string, string, error) {
	patterns := []string{"LICENSE*", "LICENCE*", "COPYING*"}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(directory, pattern))
		if err != nil {
			return "", "", err
		}
		for _, match := range matches {
			contents, err := os.ReadFile(match)
			if err != nil {
				return "", "", err
			}
			text := string(contents)
			switch {
			case strings.Contains(text, "Apache License"):
				return "Apache-2.0", match, nil
			case strings.Contains(text, "Permission is hereby granted, free of charge"):
				return "MIT", match, nil
			case strings.Contains(text, "Redistribution and use in source and binary forms"):
				return "BSD", match, nil
			case strings.Contains(text, "Permission to use, copy, modify, and/or distribute"):
				return "ISC", match, nil
			}
		}
	}
	return "", "", fmt.Errorf("no recognized license file under %s", directory)
}

func fatal(step string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", step, err)
	os.Exit(1)
}
