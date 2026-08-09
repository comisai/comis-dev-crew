package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

const sourceRepository = "https://github.com/comisai/comis.git"

type options struct {
	sourceRoot      string
	sourceCommit    string
	destinationRoot string
}

func main() {
	var config options
	flag.StringVar(&config.sourceRoot, "source-root", "", "explicit read-only Comis repository root")
	flag.StringVar(&config.sourceCommit, "source-commit", "", "full immutable Comis source commit")
	flag.StringVar(&config.destinationRoot, "destination-root", "protocol/comis", "protocol pin destination")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected positional arguments"))
	}
	if err := syncProtocol(config); err != nil {
		fatal(err)
	}
}

func syncProtocol(config options) error {
	if config.sourceRoot == "" || config.sourceCommit == "" || config.destinationRoot == "" {
		return fmt.Errorf("source root, source commit, and destination root are required")
	}
	sourceRoot, err := canonicalDirectory(config.sourceRoot)
	if err != nil {
		return err
	}
	if err := verifySourceCommit(sourceRoot, config.sourceCommit); err != nil {
		return err
	}
	sourceProtocol := filepath.Join(sourceRoot, filepath.FromSlash(bundle.SourceProtocolPath))
	sourceBundle, err := bundle.Open(sourceProtocol)
	if err != nil {
		return fmt.Errorf("verify source protocol: %w", err)
	}
	provenance := bundle.Provenance{
		SourceRepository:   sourceRepository,
		SourceCommit:       config.sourceCommit,
		SourceProtocolPath: bundle.SourceProtocolPath,
		ProtocolID:         sourceBundle.Manifest.ProtocolID,
		BundleDigest:       sourceBundle.Manifest.BundleDigest,
		Generator:          sourceBundle.Manifest.Generator,
	}
	if err := writePin(sourceBundle, provenance, config.destinationRoot); err != nil {
		return err
	}
	fmt.Printf("Pinned %s at %s from Comis commit %s.\n", provenance.ProtocolID, provenance.BundleDigest, provenance.SourceCommit)
	return nil
}

func verifySourceCommit(sourceRoot, expected string) error {
	if len(expected) != 40 || strings.ToLower(expected) != expected {
		return fmt.Errorf("source commit must be a full lowercase Git hash")
	}
	topLevel, err := runGit(sourceRoot, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	canonicalTop, err := canonicalDirectory(strings.TrimSpace(topLevel))
	if err != nil {
		return err
	}
	if canonicalTop != sourceRoot {
		return fmt.Errorf("source root is not the Git worktree root")
	}
	head, err := runGit(sourceRoot, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if strings.TrimSpace(head) != expected {
		return fmt.Errorf("source HEAD does not match requested commit")
	}
	status, err := runGit(sourceRoot, "status", "--porcelain", "--untracked-files=all", "--", bundle.SourceProtocolPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(status) != "" {
		return fmt.Errorf("source protocol tree differs from the requested commit")
	}
	return nil
}

func runGit(directory string, arguments ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("inspect Comis Git source: %w", err)
	}
	return string(output), nil
}

func writePin(source bundle.Bundle, provenance bundle.Provenance, destination string) error {
	destinationRoot, parent, err := validateDestination(destination)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".comis-protocol-sync-")
	if err != nil {
		return fmt.Errorf("create protocol staging directory: %w", err)
	}
	stagingPresent := true
	defer func() {
		if stagingPresent {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := preserveREADME(destinationRoot, temporary); err != nil {
		return err
	}
	paths := append([]string{"manifest.json"}, artifactPaths(source.Manifest.Artifacts)...)
	for _, relative := range paths {
		if err := copyRegularFile(
			filepath.Join(source.Root, filepath.FromSlash(relative)),
			filepath.Join(temporary, filepath.FromSlash(relative)),
		); err != nil {
			return fmt.Errorf("copy protocol file %q: %w", relative, err)
		}
	}
	encoded, err := json.MarshalIndent(provenance, "", "  ")
	if err != nil {
		return fmt.Errorf("encode protocol provenance: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(filepath.Join(temporary, "provenance.json"), encoded, 0o644); err != nil {
		return fmt.Errorf("write protocol provenance: %w", err)
	}
	if _, err := bundle.OpenPinned(temporary); err != nil {
		return fmt.Errorf("verify staged protocol pin: %w", err)
	}
	if err := replaceDirectory(destinationRoot, temporary, parent); err != nil {
		return err
	}
	stagingPresent = false
	return nil
}

func validateDestination(destination string) (string, string, error) {
	absolute, err := filepath.Abs(destination)
	if err != nil {
		return "", "", fmt.Errorf("resolve protocol destination: %w", err)
	}
	if filepath.Base(absolute) != "comis" || filepath.Base(filepath.Dir(absolute)) != "protocol" {
		return "", "", fmt.Errorf("protocol destination must be an explicit protocol/comis directory")
	}
	parent := filepath.Dir(absolute)
	canonicalParent, err := canonicalDirectory(parent)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize protocol destination parent: %w", err)
	}
	absolute = filepath.Join(canonicalParent, "comis")
	info, err := os.Lstat(absolute)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("inspect protocol destination: %w", err)
	}
	if err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", "", fmt.Errorf("protocol destination must be a non-symlink directory")
	}
	return absolute, canonicalParent, nil
}

func canonicalDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("canonicalize directory: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory")
	}
	return canonical, nil
}

func preserveREADME(destination, staging string) error {
	readme := filepath.Join(destination, "README.md")
	if _, err := os.Lstat(readme); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect protocol README: %w", err)
	}
	return copyRegularFile(readme, filepath.Join(staging, "README.md"))
}

func copyRegularFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func replaceDirectory(destination, staging, parent string) error {
	backup, err := os.MkdirTemp(parent, ".comis-protocol-backup-")
	if err != nil {
		return fmt.Errorf("reserve protocol backup path: %w", err)
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("prepare protocol backup path: %w", err)
	}
	hadDestination := true
	if err := os.Rename(destination, backup); errors.Is(err, os.ErrNotExist) {
		hadDestination = false
	} else if err != nil {
		return fmt.Errorf("preserve current protocol pin: %w", err)
	}
	if err := os.Rename(staging, destination); err != nil {
		if hadDestination {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("install protocol pin: %w", err)
	}
	if hadDestination {
		if err := os.RemoveAll(backup); err != nil {
			return fmt.Errorf("remove replaced protocol pin: %w", err)
		}
	}
	return nil
}

func artifactPaths(artifacts []bundle.Artifact) []string {
	paths := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		paths = append(paths, artifact.Path)
	}
	return paths
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "protocol sync failed: %v\n", err)
	os.Exit(1)
}
