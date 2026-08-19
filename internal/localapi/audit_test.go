package localapi

import "testing"

// TestAudit_ReadIsOperatorOnly keeps the security trail off the model surface.
//
// The transition stream is deliberately reachable from the MCP facade because
// it is content-free operational state. The audit trail is a different kind of
// fact: it records who was refused and whose credential was rejected, and the
// party most interested in reading it is the one it would name.
func TestAudit_ReadIsOperatorOnly(t *testing.T) {
	if !MethodReadAudit.valid() {
		t.Fatal("ReadAudit is not a declared method")
	}
	if !methodAllowed(CallerOperatorCLI, MethodReadAudit) {
		t.Error("the operator console cannot read the audit trail")
	}
	if methodAllowed(CallerMCPFacade, MethodReadAudit) {
		t.Error("the model facade can read the audit trail")
	}
	for _, caller := range []CallerClass{CallerWorkerReport, CallerComisControl} {
		if methodAllowed(caller, MethodReadAudit) {
			t.Errorf("caller %q can read the audit trail", caller)
		}
	}
	if MethodReadAudit.SideEffect() != SideEffectRead {
		t.Errorf("ReadAudit side effect = %q, want read", MethodReadAudit.SideEffect())
	}
}
