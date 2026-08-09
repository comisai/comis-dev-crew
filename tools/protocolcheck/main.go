package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func main() {
	root := flag.String("root", "protocol/comis", "pinned Comis protocol root")
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(fmt.Errorf("unexpected positional arguments"))
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

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "protocol check failed: %v\n", err)
	os.Exit(1)
}
