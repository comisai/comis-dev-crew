package conformance_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

const (
	revision3SourceCommit = "119d18edbed71c36cb35773fe752462c66385f57"
	revision3BundleDigest = "82297e6ae5ae8e2defb7f10b9962e98a3e86140c3941061584ed713a12a999ad"
)

func TestContractRevision3PinsPreparedAttachmentAuthority(t *testing.T) {
	pinned, err := bundle.OpenPinned(revision3ProtocolRoot(t))
	if err != nil {
		t.Fatalf("open revision-3 bundle: %v", err)
	}
	if pinned.Manifest.ProtocolID != "comis.capability-service/1" ||
		pinned.Manifest.BundleDigest != revision3BundleDigest ||
		pinned.Provenance.SourceCommit != revision3SourceCommit ||
		len(pinned.Manifest.Artifacts) != 28 {
		t.Fatalf("revision-3 identity = protocol:%q digest:%q source:%q artifacts:%d",
			pinned.Manifest.ProtocolID, pinned.Manifest.BundleDigest,
			pinned.Provenance.SourceCommit, len(pinned.Manifest.Artifacts))
	}

	preparation := []byte(`{"state":"prepared","externalRunRef":"external-run_attachment","registrationNonce":"registration-nonce_attachment","expiresAt":"2030-01-01T00:00:00.000Z","requestedWorkspace":{"rootHint":"/approved/workspaces/task"},"requestedAttachment":{"kind":"unix_socket","sourcePath":"/approved/runtime/task/attachment.sock"}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, preparation); err != nil {
		t.Fatalf("prepared attachment metadata rejected: %v", err)
	}

	handshake := []byte(`{"jsonrpc":"2.0","id":"operation_handshake_attachment","method":"capabilityServices.handshake","params":{"protocolId":"comis.capability-service/1","bundleDigest":"` + revision3BundleDigest + `","operationId":"operation_handshake_attachment","serviceInstanceId":"service-instance_attachment","requestedScopes":["health","evidence","report","workspace_lease","terminal_events","execution_attachment"]}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, handshake); err != nil {
		t.Fatalf("revision-3 scopes rejected: %v", err)
	}

	activation := []byte(`{"jsonrpc":"2.0","id":"operation_activate_attachment","method":"managedRuns.activate","params":{"operationId":"operation_activate_attachment","managedRunId":"managed-run_attachment","externalRunRef":"external-run_attachment","registrationNonce":"registration-nonce_attachment","workspaceLeaseId":"workspace-lease_attachment","executionAttachmentId":"execution-attachment_attachment","attachmentTargetName":"attachment-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.sock"}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, activation); err != nil {
		t.Fatalf("paired attachment activation rejected: %v", err)
	}

	missingTarget := []byte(`{"jsonrpc":"2.0","id":"operation_activate_missing_target","method":"managedRuns.activate","params":{"operationId":"operation_activate_missing_target","managedRunId":"managed-run_attachment","externalRunRef":"external-run_attachment","registrationNonce":"registration-nonce_attachment","workspaceLeaseId":"workspace-lease_attachment","executionAttachmentId":"execution-attachment_attachment"}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, missingTarget); err == nil {
		t.Fatal("activation with one attachment field was accepted")
	}
}

func TestContractRevision3RequiresActivationHandlesWhenAttachmentWasPrepared(t *testing.T) {
	boundary := semanticBoundary{operations: make(map[string]string)}
	preparation := []byte(`{"state":"prepared","externalRunRef":"external-run_attachment_join","registrationNonce":"registration-nonce_attachment_join","expiresAt":"2030-01-01T00:00:00.000Z","requestedWorkspace":{"rootHint":"/approved/workspaces/task"},"requestedAttachment":{"kind":"unix_socket","sourcePath":"/approved/runtime/task/attachment.sock"}}`)
	if kind := boundary.validate(comiswire.PayloadMCPManagedRunResult, preparation); kind != nil {
		t.Fatalf("attachment preparation rejected: %q", *kind)
	}
	activation := []byte(`{"jsonrpc":"2.0","id":"operation_activate_attachment_join","method":"managedRuns.activate","params":{"operationId":"operation_activate_attachment_join","managedRunId":"managed-run_attachment_join","externalRunRef":"external-run_attachment_join","registrationNonce":"registration-nonce_attachment_join","workspaceLeaseId":"workspace-lease_attachment_join"}}`)
	kind := boundary.validate(comiswire.PayloadRequest, activation)
	if kind == nil || *kind != comiswire.ErrorKindInvalidParams {
		t.Fatalf("missing prepared attachment handles rejection = %v", kind)
	}
}

func revision3ProtocolRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve revision-3 conformance path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "protocol", "comis"))
}
