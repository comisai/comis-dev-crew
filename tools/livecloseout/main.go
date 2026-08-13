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
	evidenceRoot := flag.String("evidence-root", "", "owner-private output root")
	flag.Parse()
	if flag.NArg() != 0 || *manifestPath == "" || *evidenceRoot == "" {
		fmt.Fprintln(os.Stderr, "livecloseout: --manifest and --evidence-root are required")
		os.Exit(2)
	}
	manifest, err := livecampaign.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "livecloseout: protected manifest is unavailable or invalid")
		os.Exit(2)
	}
	if err := livecampaign.ValidateRuntime(manifest); err != nil {
		fmt.Fprintln(os.Stderr, "livecloseout: protected runtime prerequisites are unavailable")
		os.Exit(3)
	}
	verdict, err := livecampaign.Collect(
		context.Background(), manifest, *evidenceRoot, livecampaign.RealExecutor{}, time.Now().UnixMilli(),
	)
	if err != nil || !verdict.Passed {
		fmt.Fprintln(os.Stderr, "livecloseout: closeout evidence did not pass")
		os.Exit(1)
	}
	fmt.Printf("livecloseout: passed; evidence=%s\n", verdict.EvidenceDirectory)
}
