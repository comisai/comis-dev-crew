package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

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
	if _, err := bundle.OpenPinned(root); err != nil {
		return err
	}
	want, err := generator.Generate(root)
	if err != nil {
		return err
	}
	got, err := os.ReadFile(generatedPath)
	if err != nil {
		return fmt.Errorf("read committed generated client: %w", err)
	}
	if !bytes.Equal(got, want) {
		return fmt.Errorf("generated client differs from the authenticated protocol pin; run go generate ./internal/comiswire")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "protocol check failed: %v\n", err)
	os.Exit(1)
}
