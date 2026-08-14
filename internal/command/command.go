// Package command provides the intentionally small shared scaffold behavior for
// the four product composition roots.
package command

import (
	"fmt"
	"io"
)

// Version identifies the source build unless a tagged release injects its exact
// tag through the Go linker.
var Version = "dev"

// Run handles the version and help behavior shared by each composition root.
// It returns a process exit code and performs no privileged operation.
func Run(name string, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprintf(stdout, "Usage: %s [--help] [--version]\n", name)
		fmt.Fprintln(stdout, "Pre-release scaffold: service and domain behavior are not implemented.")
		return 0
	}
	if args[0] == "--version" {
		fmt.Fprintf(stdout, "%s %s\n", name, Version)
		return 0
	}
	fmt.Fprintf(stderr, "%s: unknown argument\n", name)
	return 2
}
