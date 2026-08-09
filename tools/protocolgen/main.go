package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/comisai/comis-dev-crew/internal/comiswire/generator"
)

func main() {
	protocolRoot := flag.String("protocol-root", "protocol/comis", "verified Comis protocol pin")
	outputPath := flag.String("output", "internal/comiswire/protocol.gen.go", "generated Go output")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected positional arguments"))
	}
	if err := writeGenerated(*protocolRoot, *outputPath); err != nil {
		fatal(err)
	}
}

func writeGenerated(protocolRoot, outputPath string) error {
	if protocolRoot == "" || outputPath == "" {
		return fmt.Errorf("protocol root and output path are required")
	}
	source, err := generator.Generate(protocolRoot)
	if err != nil {
		return err
	}
	info, err := os.Lstat(outputPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect generated output: %w", err)
	}
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("generated output is not a regular file")
	}
	parent := filepath.Dir(outputPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create generated output directory: %w", err)
	}
	temporary, err := os.CreateTemp(parent, ".protocol-gen-")
	if err != nil {
		return fmt.Errorf("create generated output: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set generated output mode: %w", err)
	}
	if _, err := temporary.Write(source); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write generated output: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close generated output: %w", err)
	}
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return fmt.Errorf("install generated output: %w", err)
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "protocol generation failed: %v\n", err)
	os.Exit(1)
}
