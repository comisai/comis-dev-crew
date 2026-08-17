//go:build linux

package reporter

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestLinuxRuntimeIdentityHelpersRejectInvalidDescriptors(t *testing.T) {
	if err := syncRuntimeDirectory(-1); err == nil {
		t.Fatal("syncRuntimeDirectory accepted an invalid descriptor")
	}
	if _, err := runtimePinnedStatIdentity(-1, "missing", unix.Stat_t{}); err == nil {
		t.Fatal("runtimePinnedStatIdentity accepted an invalid descriptor")
	}
	if mountID, err := runtimeDescriptorMountID(-1); err == nil || mountID != 0 {
		t.Fatalf("runtimeDescriptorMountID(invalid) = %d, %v", mountID, err)
	}
	if _, err := runtimePinnedSocketIdentity(-1, "missing"); err == nil {
		t.Fatal("runtimePinnedSocketIdentity accepted an invalid descriptor")
	}
	if _, err := runtimeRemovalPinIdentity(&runtimeRemovalPin{descriptor: -1}, unix.Stat_t{}); err == nil {
		t.Fatal("runtimeRemovalPinIdentity accepted an invalid identity")
	}
}
