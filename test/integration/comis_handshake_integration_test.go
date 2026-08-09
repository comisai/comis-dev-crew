//go:build integration

package integration_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/comiswire"
)

func TestComisFixtureHost_RealSocketHandshakeAndAuthentication(t *testing.T) {
	comisRoot := os.Getenv("COMIS_FIXTURE_HOST_ROOT")
	if comisRoot == "" {
		t.Skip("COMIS_FIXTURE_HOST_ROOT is required for the cross-repository fixture-host test")
	}
	canonicalRoot, err := filepath.EvalSymlinks(comisRoot)
	if err != nil || !filepath.IsAbs(comisRoot) || filepath.Clean(comisRoot) != comisRoot || canonicalRoot != comisRoot {
		t.Fatalf("COMIS_FIXTURE_HOST_ROOT must be an existing canonical absolute path: %v", err)
	}
	fixtureDirectory := integrationShortTempDir(t)
	entryPath := filepath.Join(comisRoot, "packages", "daemon", "src", "__tests__", "capability-service-protocol-fixture-host-entry.ts")
	command := exec.Command("node", "--import", "tsx", entryPath, "--directory", fixtureDirectory, "--service-instance-id", "service-instance_go")
	command.Dir = comisRoot
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("fixture host stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("start Comis fixture host: %v", err)
	}
	hostStopped := false
	t.Cleanup(func() {
		if hostStopped || command.Process == nil {
			return
		}
		_ = command.Process.Signal(syscall.SIGTERM)
		_ = command.Wait()
	})

	readyLine, err := bufio.NewReader(stdout).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read fixture host readiness: %v: %s", err, stderr.String())
	}
	var ready struct {
		ProtocolID        string `json:"protocolId"`
		BundleDigest      string `json:"bundleDigest"`
		ServiceInstanceID string `json:"serviceInstanceId"`
		SocketPath        string `json:"socketPath"`
		CredentialSource  struct {
			Kind string `json:"kind"`
			Path string `json:"path"`
		} `json:"credentialSource"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(readyLine), &ready); err != nil {
		t.Fatalf("decode fixture readiness: %v", err)
	}
	if ready.ProtocolID != comiswire.ProtocolID || ready.BundleDigest != comiswire.BundleDigest ||
		ready.ServiceInstanceID != "service-instance_go" || ready.CredentialSource.Kind != "file" {
		t.Fatalf("fixture authority = %#v, want exact pinned protocol and instance", ready)
	}
	credentialBytes, err := os.ReadFile(ready.CredentialSource.Path)
	if err != nil {
		t.Fatalf("read fixture credential source: %v", err)
	}
	credential := strings.TrimSpace(string(credentialBytes))
	if credential == "" || bytes.Contains(readyLine, []byte(credential)) {
		t.Fatal("fixture readiness exposed or omitted the protected credential")
	}

	client, err := comiswire.NewAuthenticatedUnixClient(ready.SocketPath, credential, 5*time.Second)
	if err != nil {
		t.Fatalf("create generated authenticated client: %v", err)
	}
	result, err := client.Handshake(context.Background(), comiswire.HandshakeRequestParams{
		ProtocolID: comiswire.ProtocolID, BundleDigest: comiswire.BundleDigest,
		OperationID: "operation_go_handshake", ServiceInstanceID: comiswire.ServiceInstanceID(ready.ServiceInstanceID),
		RequestedScopes: []comiswire.ServiceScope{comiswire.ServiceScopeHealth, comiswire.ServiceScopeReport},
	})
	if err != nil || result.ProtocolID != comiswire.ProtocolID || result.BundleDigest != comiswire.BundleDigest ||
		result.ServiceInstanceID != comiswire.ServiceInstanceID(ready.ServiceInstanceID) {
		t.Fatalf("generated Handshake() = %#v, %v, want exact host authority", result, err)
	}

	alteredRequest := comiswire.HandshakeRequest{
		JSONRPC: comiswire.JSONRPCVersion, ID: "operation_go_altered_digest", Method: comiswire.MethodCapabilityServicesHandshake,
		Params: comiswire.HandshakeRequestParams{
			ProtocolID: comiswire.ProtocolID, BundleDigest: strings.Repeat("0", 64),
			OperationID: "operation_go_altered_digest", ServiceInstanceID: comiswire.ServiceInstanceID(ready.ServiceInstanceID),
			RequestedScopes: []comiswire.ServiceScope{comiswire.ServiceScopeHealth},
		},
	}
	alteredResponse := callAuthenticatedHandshake(t, ready.SocketPath, credential, alteredRequest)
	if alteredResponse.Error.Kind != comiswire.ErrorKindBundleDigestMismatch {
		t.Fatalf("altered digest response = %#v, want bundle_digest_mismatch", alteredResponse)
	}

	wrongClient, err := comiswire.NewAuthenticatedUnixClient(ready.SocketPath, strings.Repeat("x", 43), 5*time.Second)
	if err != nil {
		t.Fatalf("create wrong-credential client: %v", err)
	}
	_, err = wrongClient.Handshake(context.Background(), comiswire.HandshakeRequestParams{
		ProtocolID: comiswire.ProtocolID, BundleDigest: comiswire.BundleDigest,
		OperationID: "operation_go_wrong_credential", ServiceInstanceID: comiswire.ServiceInstanceID(ready.ServiceInstanceID),
		RequestedScopes: []comiswire.ServiceScope{comiswire.ServiceScopeHealth},
	})
	var remote comiswire.RPCError
	if !errors.As(err, &remote) || remote.Kind != comiswire.ErrorKindUnauthorizedInstance {
		t.Fatalf("wrong credential error = %#v, %v, want unauthorized_instance", remote, err)
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("stop fixture host: %v", err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("wait fixture host: %v: %s", err, stderr.String())
	}
	hostStopped = true
	if _, err := os.Lstat(ready.SocketPath); !os.IsNotExist(err) {
		t.Fatalf("fixture socket remained after shutdown: %v", err)
	}
	if _, err := os.Lstat(ready.CredentialSource.Path); !os.IsNotExist(err) {
		t.Fatalf("fixture credential remained after shutdown: %v", err)
	}
}

type authenticatedHandshakeFrame struct {
	comiswire.HandshakeRequest
	Bearer string `json:"bearer"`
}

func callAuthenticatedHandshake(t *testing.T, socketPath, bearer string, request comiswire.HandshakeRequest) comiswire.ErrorResponse {
	t.Helper()
	connection, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		t.Fatalf("dial fixture socket: %v", err)
	}
	defer connection.Close()
	encoded, err := json.Marshal(authenticatedHandshakeFrame{HandshakeRequest: request, Bearer: bearer})
	if err != nil {
		t.Fatalf("encode authenticated handshake: %v", err)
	}
	if _, err := io.Copy(connection, bytes.NewReader(append(encoded, '\n'))); err != nil {
		t.Fatalf("write authenticated handshake: %v", err)
	}
	var response comiswire.ErrorResponse
	if err := json.NewDecoder(connection).Decode(&response); err != nil {
		t.Fatalf("decode authenticated handshake error: %v", err)
	}
	return response
}
