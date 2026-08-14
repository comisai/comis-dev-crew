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
	resourceBaselinePath := flag.String("resource-baseline", "", "owner-private one-hour start sample")
	flag.Parse()
	if flag.NArg() != 0 || *manifestPath == "" || *evidenceRoot == "" || *resourceBaselinePath == "" {
		fmt.Fprintln(os.Stderr, "livecloseout: --manifest, --evidence-root, and --resource-baseline are required")
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
	baseline, err := livecampaign.LoadResourceBaseline(*resourceBaselinePath)
	if err != nil || livecampaign.VerifyResourceBaseline(manifest, baseline) != nil {
		fmt.Fprintln(os.Stderr, "livecloseout: resource baseline is unavailable or differs from the campaign")
		os.Exit(2)
	}
	capturedAtMs := time.Now().UnixMilli()
	finished, err := livecampaign.CaptureResourceSnapshot(
		context.Background(), manifest, livecampaign.RealExecutor{}, capturedAtMs,
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "livecloseout: finishing resource snapshot is unavailable")
		os.Exit(1)
	}
	verdict, err := livecampaign.Collect(
		context.Background(), manifest, *evidenceRoot, livecampaign.RealExecutor{}, capturedAtMs,
		livecampaign.ResourceObservation{SchemaVersion: 1, Started: baseline.Snapshot, Finished: finished},
	)
	if err != nil || !verdict.Passed {
		fmt.Fprintln(os.Stderr, "livecloseout: closeout evidence did not pass")
		os.Exit(1)
	}
	fmt.Printf("livecloseout: passed; evidence=%s\n", verdict.EvidenceDirectory)
}
