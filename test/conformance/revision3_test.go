package conformance_test

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
	"github.com/comisai/comis-dev-crew/internal/comiswire/bundle"
)

const (
	pinnedSourceCommit = "4deb33ed59b272d4a84046a20a7f51a615f06039"
	pinnedBundleDigest = "dea251a955a4d68faf402aa6977db1b4544737e43aa1f624f39dc359008f6414"
)

func TestContractPinsPreparedAttachmentAuthority(t *testing.T) {
	pinned, err := bundle.OpenPinned(pinnedProtocolRoot(t))
	if err != nil {
		t.Fatalf("open pinned bundle: %v", err)
	}
	if pinned.Manifest.ProtocolID != "comis.capability-service/1" ||
		pinned.Manifest.BundleDigest != pinnedBundleDigest ||
		pinned.Provenance.SourceCommit != pinnedSourceCommit ||
		len(pinned.Manifest.Artifacts) != 43 {
		t.Fatalf("pinned identity = protocol:%q digest:%q source:%q artifacts:%d",
			pinned.Manifest.ProtocolID, pinned.Manifest.BundleDigest,
			pinned.Provenance.SourceCommit, len(pinned.Manifest.Artifacts))
	}

	preparation := []byte(`{"state":"prepared","externalRunRef":"external-run_attachment","registrationNonce":"registration-nonce_attachment","expiresAt":"2030-01-01T00:00:00.000Z","requestedWorkspace":{"rootHint":"/approved/workspaces/task"},"requestedAttachment":{"kind":"unix_socket","sourcePath":"/approved/runtime/task/attachment.sock","relayIdentity":"abababababababababababababababababababababababababababababababab"}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, preparation); err != nil {
		t.Fatalf("prepared attachment metadata rejected: %v", err)
	}
	validIdentity := strings.Repeat("ab", 32)
	for name, invalid := range map[string][]byte{
		"missing":   []byte(strings.Replace(string(preparation), `,"relayIdentity":"`+validIdentity+`"`, "", 1)),
		"all zero":  []byte(strings.Replace(string(preparation), validIdentity, strings.Repeat("0", 64), 1)),
		"uppercase": []byte(strings.Replace(string(preparation), validIdentity, strings.ToUpper(validIdentity), 1)),
	} {
		t.Run("rejects "+name+" relay identity", func(t *testing.T) {
			if err := comiswire.ValidatePayload(comiswire.PayloadMCPManagedRunResult, invalid); err == nil {
				t.Fatal("invalid prepared attachment relay identity was accepted")
			}
		})
	}

	handshake := []byte(`{"jsonrpc":"2.0","id":"operation_handshake_attachment","method":"capabilityServices.handshake","params":{"protocolId":"comis.capability-service/1","bundleDigest":"` + pinnedBundleDigest + `","operationId":"operation_handshake_attachment","serviceInstanceId":"service-instance_attachment","requestedScopes":["health","attention_response","evidence","report","workspace_lease","terminal_events","execution_attachment","managed_run_group","approval_receipt"]}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, handshake); err != nil {
		t.Fatalf("pinned scopes rejected: %v", err)
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

func TestContractRequiresPreparedMemberIdentitiesForGroupAbandon(t *testing.T) {
	abandon := []byte(`{"jsonrpc":"2.0","id":"operation_group_abandon","method":"managedRunGroups.abandon","params":{"operationId":"operation_group_abandon","managedRunGroupId":"managed-run-group_abandon","registrationNonce":"group-registration-nonce_abandon","members":[{"managedRunId":"managed-run_abandon","externalRunRef":"external-run_abandon","registrationNonce":"member-registration-nonce_abandon"}],"reason":"activation_rejected","disposition":"reap_safe"}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, abandon); err != nil {
		t.Fatalf("group abandon with exact member identities rejected: %v", err)
	}

	withoutMembers := []byte(`{"jsonrpc":"2.0","id":"operation_group_abandon","method":"managedRunGroups.abandon","params":{"operationId":"operation_group_abandon","managedRunGroupId":"managed-run-group_abandon","registrationNonce":"group-registration-nonce_abandon","reason":"activation_rejected","disposition":"reap_safe"}}`)
	if err := comiswire.ValidatePayload(comiswire.PayloadRequest, withoutMembers); err == nil {
		t.Fatal("group abandon without prepared member identities was accepted")
	}
}

func TestContractRequiresActivationHandlesWhenAttachmentWasPrepared(t *testing.T) {
	boundary := semanticBoundary{operations: make(map[string]string)}
	preparation := []byte(`{"state":"prepared","externalRunRef":"external-run_attachment_join","registrationNonce":"registration-nonce_attachment_join","expiresAt":"2030-01-01T00:00:00.000Z","requestedWorkspace":{"rootHint":"/approved/workspaces/task"},"requestedAttachment":{"kind":"unix_socket","sourcePath":"/approved/runtime/task/attachment.sock","relayIdentity":"abababababababababababababababababababababababababababababababab"}}`)
	if kind := boundary.validate(comiswire.PayloadMCPManagedRunResult, preparation); kind != nil {
		t.Fatalf("attachment preparation rejected: %q", *kind)
	}
	activation := []byte(`{"jsonrpc":"2.0","id":"operation_activate_attachment_join","method":"managedRuns.activate","params":{"operationId":"operation_activate_attachment_join","managedRunId":"managed-run_attachment_join","externalRunRef":"external-run_attachment_join","registrationNonce":"registration-nonce_attachment_join","workspaceLeaseId":"workspace-lease_attachment_join"}}`)
	kind := boundary.validate(comiswire.PayloadRequest, activation)
	if kind == nil || *kind != comiswire.ErrorKindInvalidParams {
		t.Fatalf("missing prepared attachment handles rejection = %v", kind)
	}
}

func pinnedProtocolRoot(t *testing.T) string {
	t.Helper()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve pinned conformance path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", "..", "protocol", "comis"))
}
