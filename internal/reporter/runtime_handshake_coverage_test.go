package reporter

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"errors"
	"net"
	"strings"
	"testing"
)

func TestRuntimeHandshakeRejectsMalformedRelayAuthority(t *testing.T) {
	if (*RuntimeServer)(nil).RelayIdentity() != "" {
		t.Fatal("nil server exposed a relay identity")
	}
	if _, _, err := runtimeRelayIdentity(nil); err == nil {
		t.Fatal("empty relay seed acquired identity")
	}
	if _, _, err := runtimeRelayIdentity(make([]byte, runtimeRelaySeedBytes)); err == nil {
		t.Fatal("zero relay seed acquired identity")
	}
	seed := bytes.Repeat([]byte{1}, runtimeRelaySeedBytes)
	identity, privateKey, err := runtimeRelayIdentity(seed)
	if err != nil || len(privateKey) != ed25519.PrivateKeySize {
		t.Fatalf("runtimeRelayIdentity() = %q, %d, %v", identity, len(privateKey), err)
	}
	if publicKey, err := parseRuntimeRelayIdentity(identity); err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("parseRuntimeRelayIdentity() = %x, %v", publicKey, err)
	}
	if _, err := parseRuntimeRelayIdentity(strings.ToUpper(identity)); err == nil {
		t.Fatal("uppercase relay identity was accepted")
	}

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() { _, _ = client.Write([]byte("invalid authentication\n")) }()
	if _, err := authenticateRuntimeServerConnection(server, bufio.NewReader(server), privateKey); err == nil {
		t.Fatal("server accepted malformed authentication")
	}

	server, client = net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		reader := bufio.NewReader(server)
		_, _ = reader.ReadString('\n')
		_, _ = server.Write([]byte("invalid proof\n"))
	}()
	publicKey, err := parseRuntimeRelayIdentity(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := authenticateRuntimeClientConnection(client, publicKey); err == nil {
		t.Fatal("client accepted malformed relay proof")
	}
}

func TestRuntimeFramesBindCiphertextDirectionAndLimits(t *testing.T) {
	session, err := newRuntimeSession([]byte("shared"), []byte("transcript"))
	if err != nil {
		t.Fatal(err)
	}
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	writeDone := make(chan error, 1)
	go func() { writeDone <- writeRuntimeFrame(server, session, runtimeRequestDirection, []byte("request")) }()
	plaintext, err := readRuntimeFrame(
		bufio.NewReaderSize(client, runtimeFrameBufferSize(32)), session, runtimeRequestDirection, 32,
	)
	if err != nil || string(plaintext) != "request" {
		t.Fatalf("readRuntimeFrame() = %q, %v", plaintext, err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}

	server, client = net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() { _ = writeRuntimeFrame(server, session, runtimeRequestDirection, []byte("request")) }()
	if _, err := readRuntimeFrame(
		bufio.NewReaderSize(client, runtimeFrameBufferSize(32)), session, runtimeResponseDirection, 32,
	); err == nil {
		t.Fatal("frame opened under the wrong direction")
	}
	if _, err := readRuntimeFrame(bufio.NewReader(strings.NewReader("!\n")), session, runtimeRequestDirection, 32); err == nil {
		t.Fatal("invalid base64 frame was accepted")
	}
	if _, err := readRuntimeFrame(bufio.NewReader(strings.NewReader("\n")), session, runtimeRequestDirection, 32); err == nil {
		t.Fatal("empty frame was accepted")
	}
	oversized := strings.Repeat("A", runtimeFrameBufferSize(1)+8) + "\n"
	if _, err := readRuntimeFrame(
		bufio.NewReaderSize(strings.NewReader(oversized), runtimeFrameBufferSize(1)), session, runtimeRequestDirection, 1,
	); !errors.Is(err, errRuntimeFrameTooLarge) {
		t.Fatalf("oversized frame error = %v", err)
	}
	if len(runtimeFrameAdditionalData(runtimeRequestDirection)) == 0 || runtimeFrameBufferSize(32) <= 32 {
		t.Fatal("runtime frame bounds were not encoded")
	}
}
