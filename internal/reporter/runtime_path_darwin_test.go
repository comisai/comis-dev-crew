//go:build darwin

package reporter

import "testing"

func TestProtectedRuntimeMountFailsClosedWithoutDarwinMountInstanceIdentity(t *testing.T) {
	pinned, err := pinRuntimeMountDirectory(t.TempDir())
	if pinned != nil {
		_ = pinned.close()
	}
	if err == nil {
		t.Fatal("protected runtime mount accepted filesystem identity as mount-instance authority")
	}
}
