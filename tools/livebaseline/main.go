package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/comisai/comis-dev-crew/test/support/livecampaign"
)

func main() {
	manifestPath := flag.String("manifest", "", "owner-private protected campaign manifest")
	outputPath := flag.String("output", "", "new owner-private resource baseline artifact")
	flag.Parse()
	if flag.NArg() != 0 || *manifestPath == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "livebaseline: --manifest and --output are required")
		os.Exit(2)
	}
	manifest, err := livecampaign.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "livebaseline: protected manifest is unavailable or invalid")
		os.Exit(2)
	}
	if err := livecampaign.ValidateRuntime(manifest); err != nil {
		fmt.Fprintln(os.Stderr, "livebaseline: protected runtime prerequisites are unavailable")
		os.Exit(3)
	}
	snapshot, err := livecampaign.CaptureResourceSnapshot(
		context.Background(), manifest, livecampaign.RealExecutor{}, time.Now().UnixMilli(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "livebaseline: starting resource snapshot is unavailable")
		os.Exit(1)
	}
	baseline, err := livecampaign.NewResourceBaseline(manifest, snapshot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "livebaseline: starting resource snapshot did not prove both task lanes")
		os.Exit(1)
	}
	if err := livecampaign.WriteResourceBaseline(*outputPath, baseline); err != nil {
		fmt.Fprintln(os.Stderr, "livebaseline: resource baseline could not be created")
		os.Exit(1)
	}
	fmt.Printf("livebaseline: captured; artifact=%s\n", *outputPath)
}
