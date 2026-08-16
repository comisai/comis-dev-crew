package reporter

import (
	"bytes"
	"testing"
)

func boundaryRuntimeRelaySeed() []byte {
	return bytes.Repeat([]byte{0x5a}, runtimeRelaySeedBytes)
}

func boundaryRuntimeRelayIdentity(t *testing.T) string {
	t.Helper()
	identity, _, err := runtimeRelayIdentity(boundaryRuntimeRelaySeed())
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func configureBoundaryRuntimeRelay(t *testing.T, server *RuntimeServer) {
	t.Helper()
	identity, privateKey, err := runtimeRelayIdentity(boundaryRuntimeRelaySeed())
	if err != nil {
		t.Fatal(err)
	}
	server.relayIdentity = identity
	server.relayPrivateKey = privateKey
}
