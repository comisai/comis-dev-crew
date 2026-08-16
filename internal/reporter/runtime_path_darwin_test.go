//go:build darwin

package reporter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProtectedRuntimeMountFailsClosedWithoutDarwinMountInstanceIdentity(t *testing.T) {
	pinned, err := pinProtectedRuntimeMountDirectory(t.TempDir())
	if pinned != nil {
		_ = pinned.close()
	}
	if err == nil {
		t.Fatal("protected runtime mount accepted filesystem identity as mount-instance authority")
	}
}

func TestDarwinSourceRuntimeRelayUsesPinnedNodeIdentity(t *testing.T) {
	socketPath := filepath.Join(boundaryRuntimeDirectory(t), "attachment.sock")
	server, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: socketPath,
		Brief:      boundaryBrief("task-darwin-source-relay"),
		Reporter:   &Client{},
		RelaySeed:  boundaryRuntimeRelaySeed(),
	})
	if err != nil {
		t.Fatalf("ListenRuntime(source socket) = %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	if err := server.Close(); err != nil {
		t.Fatalf("RuntimeServer.Close(source socket) = %v", err)
	}
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("source socket after close error = %v, want not exist", err)
	}
}
