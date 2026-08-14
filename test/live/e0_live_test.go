//go:build live

package live_test

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/test/support/livecampaign"
)

func TestE0LiveCampaign_RealTelegramProtectedLinuxCloseout(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatalf("protected E0 campaign requires the dedicated Linux host, got %s", runtime.GOOS)
	}
	manifestPath := os.Getenv("DEVCREW_LIVE_MANIFEST")
	evidenceRoot := os.Getenv("DEVCREW_LIVE_EVIDENCE_ROOT")
	backupRoot := os.Getenv("DEVCREW_LIVE_BACKUP_ROOT")
	restoreRoot := os.Getenv("DEVCREW_LIVE_RESTORE_ROOT")
	freshInstallRoot := os.Getenv("DEVCREW_LIVE_FRESH_INSTALL_ROOT")
	upgradeRoot := os.Getenv("DEVCREW_LIVE_UPGRADE_ROOT")
	if manifestPath == "" || evidenceRoot == "" || backupRoot == "" || restoreRoot == "" ||
		freshInstallRoot == "" || upgradeRoot == "" {
		t.Fatal("protected E0 campaign requires manifest, evidence, backup, restore, fresh-install, and upgrade roots")
	}
	manifest, err := livecampaign.LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("load protected campaign manifest: %v", err)
	}
	if err := livecampaign.ValidateRuntime(manifest); err != nil {
		t.Fatalf("validate protected campaign runtime: %v", err)
	}
	runner := livecampaign.CampaignRunner{
		Executor: livecampaign.RealExecutor{}, PollInterval: 5 * time.Second,
		CaptureResources: livecampaign.CaptureResourceSnapshot,
		VerifyRecovery: func(
			ctx context.Context, manifest livecampaign.Manifest, executor livecampaign.Executor, capturedAtMs int64,
		) (livecampaign.RecoveryEvidence, error) {
			return livecampaign.RunRecoveryVerification(
				ctx, manifest, backupRoot, restoreRoot, freshInstallRoot, upgradeRoot, executor,
				livecampaign.RealRollbackServiceProbe, capturedAtMs,
			)
		},
		NowMs: func() int64 { return time.Now().UnixMilli() }, Logf: t.Logf,
	}
	verdict, err := runner.Run(context.Background(), manifest, evidenceRoot)
	if err != nil {
		t.Fatalf("run protected campaign: %v", err)
	}
	if !verdict.Passed {
		t.Fatalf("protected campaign closeout did not pass: %#v", verdict)
	}
	t.Logf("protected campaign passed; evidence=%s", verdict.EvidenceDirectory)
}
