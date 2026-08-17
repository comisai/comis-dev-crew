package reporter

import (
	"bufio"
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"net"
	"testing"
)

func handshakeRelayKeys(t *testing.T) (string, ed25519.PrivateKey) {
	t.Helper()
	identity, privateKey, err := runtimeRelayIdentity(bytes.Repeat([]byte{7}, runtimeRelaySeedBytes))
	if err != nil {
		t.Fatal(err)
	}
	return identity, privateKey
}

func handshakeClientRequestLine(t *testing.T) string {
	t.Helper()
	clientPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, runtimeRelayNonceBytes)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return runtimeProtocolVersion + " auth " + hex.EncodeToString(nonce) + " " +
		hex.EncodeToString(clientPrivate.PublicKey().Bytes()) + "\n"
}

func TestRuntimeServerHandshakeRejectsUndecodableExchangeMaterial(t *testing.T) {
	_, privateKey := handshakeRelayKeys(t)
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	request := runtimeProtocolVersion + " auth zz " + hex.EncodeToString(make([]byte, runtimeRelayExchangeBytes)) + "\n"
	go func() { _, _ = client.Write([]byte(request)) }()
	if _, err := authenticateRuntimeServerConnection(server, bufio.NewReader(server), privateKey); err == nil {
		t.Fatal("server accepted an undecodable relay nonce")
	}
}

func TestRuntimeServerHandshakeFailsWhenProofCannotBeDelivered(t *testing.T) {
	_, privateKey := handshakeRelayKeys(t)
	server, client := net.Pipe()
	defer server.Close()
	request := handshakeClientRequestLine(t)
	go func() {
		_, _ = client.Write([]byte(request))
		_ = client.Close()
	}()
	if _, err := authenticateRuntimeServerConnection(server, bufio.NewReader(server), privateKey); err == nil {
		t.Fatal("server reported success after an undeliverable relay proof")
	}
}

func TestRuntimeClientHandshakeFailsWhenRequestCannotBeDelivered(t *testing.T) {
	identity, _ := handshakeRelayKeys(t)
	publicKey, err := parseRuntimeRelayIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	_ = server.Close()
	_ = client.Close()
	if _, err := authenticateRuntimeClientConnection(client, publicKey); err == nil {
		t.Fatal("client reported success after an undeliverable relay request")
	}
}

func TestRuntimeClientHandshakeRejectsWellFormedUnsignedProof(t *testing.T) {
	identity, _ := handshakeRelayKeys(t)
	publicKey, err := parseRuntimeRelayIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	serverPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		reader := bufio.NewReader(server)
		_, _ = reader.ReadString('\n')
		proof := runtimeProtocolVersion + " proof " + hex.EncodeToString(serverPrivate.PublicKey().Bytes()) + " " +
			hex.EncodeToString(make([]byte, runtimeRelaySignatureBytes)) + "\n"
		_, _ = server.Write([]byte(proof))
	}()
	if _, err := authenticateRuntimeClientConnection(client, publicKey); err == nil {
		t.Fatal("client accepted a relay proof that was not signed by the pinned identity")
	}
}
