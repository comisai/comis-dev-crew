package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
	"github.com/comisai/comis-dev-crew/internal/comiswire/generator"
)

func main() {
	root := flag.String("root", "protocol/comis", "pinned Comis protocol root")
	generated := flag.String("generated", "internal/comiswire/protocol.gen.go", "committed generated Go client")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected positional arguments"))
	}
	if err := check(*root, *generated); err != nil {
		fatal(err)
	}
	pinned, err := bundle.OpenPinned(*root)
	if err != nil {
		fatal(err)
	}
	fmt.Printf(
		"Protocol pin matches %s at %s from Comis commit %s.\n",
		pinned.Manifest.ProtocolID,
		pinned.Manifest.BundleDigest,
		pinned.Provenance.SourceCommit,
	)
}

func check(root, generatedPath string) error {
	return checkWithTemporaryParent(root, generatedPath, "")
}

func checkWithTemporaryParent(root, generatedPath, temporaryParent string) error {
	if _, err := bundle.OpenPinned(root); err != nil {
		return err
	}
	temporaryRoot, err := os.MkdirTemp(temporaryParent, "comis-protocol-check-")
	if err != nil {
		return fmt.Errorf("create temporary protocol regeneration tree: %w", err)
	}
	checkErr := checkInTemporaryTree(root, generatedPath, temporaryRoot)
	removeErr := os.RemoveAll(temporaryRoot)
	if removeErr != nil {
		removeErr = fmt.Errorf("remove temporary protocol regeneration tree: %w", removeErr)
	}
	return errors.Join(checkErr, removeErr)
}

func checkInTemporaryTree(root, generatedPath, temporaryRoot string) error {
	want, err := generator.Generate(root)
	if err != nil {
		return err
	}
	temporaryGenerated := filepath.Join(temporaryRoot, "internal", "comiswire", "protocol.gen.go")
	if err := os.MkdirAll(filepath.Dir(temporaryGenerated), 0o700); err != nil {
		return fmt.Errorf("create temporary generated client directory: %w", err)
	}
	if err := os.WriteFile(temporaryGenerated, want, 0o600); err != nil {
		return fmt.Errorf("write temporary generated client: %w", err)
	}
	regenerated, err := os.ReadFile(temporaryGenerated)
	if err != nil {
		return fmt.Errorf("read temporary generated client: %w", err)
	}
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		return fmt.Errorf("read committed generated client: %w", err)
	}
	if !bytes.Equal(got, regenerated) {
		return fmt.Errorf("generated client differs from the authenticated protocol pin; run go generate ./internal/comiswire")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "protocol check failed: %v\n", err)
	os.Exit(1)
}
