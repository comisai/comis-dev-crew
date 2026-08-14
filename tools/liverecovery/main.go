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
	backupRoot := flag.String("backup-root", "", "new owner-private recovery backup root")
	restoreRoot := flag.String("restore-root", "", "new isolated restore root")
	outputPath := flag.String("output", "", "new owner-private recovery evidence artifact")
	flag.Parse()
	if flag.NArg() != 0 || *manifestPath == "" || *backupRoot == "" || *restoreRoot == "" || *outputPath == "" {
		fmt.Fprintln(os.Stderr, "liverecovery: --manifest, --backup-root, --restore-root, and --output are required")
		os.Exit(2)
	}
	manifest, err := livecampaign.LoadManifest(*manifestPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "liverecovery: protected manifest is unavailable or invalid")
		os.Exit(2)
	}
	if err := livecampaign.ValidateRuntime(manifest); err != nil {
		fmt.Fprintln(os.Stderr, "liverecovery: protected runtime prerequisites are unavailable")
		os.Exit(3)
	}
	evidence, err := livecampaign.RunRecoveryVerification(
		context.Background(), manifest, *backupRoot, *restoreRoot, livecampaign.RealExecutor{},
		livecampaign.RealRollbackServiceProbe, time.Now().UnixMilli(),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "liverecovery: backup, restore, or rollback evidence did not pass")
		os.Exit(1)
	}
	if err := livecampaign.WriteRecoveryEvidence(*outputPath, evidence); err != nil {
		fmt.Fprintln(os.Stderr, "liverecovery: recovery evidence artifact could not be created")
		os.Exit(1)
	}
	fmt.Printf("liverecovery: passed; evidence=%s\n", *outputPath)
}
