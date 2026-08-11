package conformance_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

func TestPinnedContractRequiresExactManagedRunRelease(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve conformance path")
	}
	protocolRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "protocol", "comis"))
	pinned, err := bundle.OpenPinned(protocolRoot)
	if err != nil {
		t.Fatalf("open pinned Comis protocol bundle: %v", err)
	}
	var found bool
	for _, method := range pinned.Manifest.MethodCatalog {
		if method.Name != "managedRuns.release" {
			continue
		}
		found = true
		if method.Direction != "service-to-comis" || method.RequiredServiceScope == nil ||
			*method.RequiredServiceScope != "workspace_lease" {
			t.Fatalf("release method authority = %#v", method)
		}
	}
	if !found {
		t.Fatal("managedRuns.release is absent from the pinned catalog")
	}
	payload := []byte(`{"jsonrpc":"2.0","id":"operation_release","method":"managedRuns.release","params":{"operationId":"operation_release","managedRunId":"managed-run_a","workspaceLeaseId":"workspace-lease_a","disposition":"reap_safe","releasedAtMs":1800000000000}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, payload); err != nil {
		t.Fatalf("managed run release request rejected: %v", err)
	}
}
