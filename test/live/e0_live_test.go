//go:build live

package live_test

import (
	"os"
	"testing"
)

func TestE0LiveCampaign_RequiresProtectedEnvironmentAndImplementation(t *testing.T) {
	required := []string{
		"DEVCREW_LIVE_COMIS_SOCKET",
		"DEVCREW_LIVE_TELEGRAM_THREAD",
		"DEVCREW_LIVE_GITHUB_REPOSITORY",
		"DEVCREW_LIVE_WORKER_PROFILE",
	}
	for _, name := range required {
		if os.Getenv(name) == "" {
			t.Fatalf("protected live prerequisite %s is unavailable", name)
		}
	}
	t.Fatal("E0 protected live campaign is not implemented; release readiness is unavailable")
}
