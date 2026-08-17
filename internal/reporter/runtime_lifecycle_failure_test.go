package reporter

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListenRuntimeRequiresRelayIdentitySeed(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	_, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: filepath.Join(root, "attachment.sock"),
		Brief:      boundaryBrief("task-boundary-0001"),
		Reporter:   &Client{},
	})
	if err == nil || !strings.Contains(err.Error(), "relay identity is required") {
		t.Fatalf("ListenRuntime(absent relay seed) error = %v", err)
	}
}

func TestRuntimeSocketTargetRejectsUncanonicalParent(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(real, "runtime")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	err := validateRuntimeSocketTarget(filepath.Join(link, "runtime", "attachment.sock"))
	if err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("validateRuntimeSocketTarget(uncanonical parent) error = %v", err)
	}
}

func TestBindLaunchRejectsUnavailableServerAndBinding(t *testing.T) {
	if err := (*RuntimeServer)(nil).BindLaunch(RuntimeLaunchConfig{}); err == nil {
		t.Fatal("nil server accepted a launch binding")
	}
	if err := (&RuntimeServer{}).BindLaunch(RuntimeLaunchConfig{}); err == nil {
		t.Fatal("server without a reporter accepted a launch binding")
	}
	server := &RuntimeServer{reporter: &Client{}}
	if err := server.BindLaunch(RuntimeLaunchConfig{OperationID: "not-an-operation"}); err == nil {
		t.Fatal("server accepted an invalid launch binding")
	}
}

func TestRuntimeClientCallsFailWhenAttachmentIsUnreachable(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listenBoundarySocket(t, socketPath, 0o600)
	client, err := NewRuntimeClient(socketPath, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Report(ctx, boundaryReport(boundaryBrief("task-boundary-0001"))); err == nil {
		t.Fatal("report reached a removed attachment socket")
	}
	if err := client.Acknowledge(ctx, root); err == nil {
		t.Fatal("acknowledgement reached a removed attachment socket")
	}
	if _, err := client.Brief(ctx); err == nil {
		t.Fatal("brief read reached a removed attachment socket")
	}
}

func TestRuntimeClientCallRejectsUnauthenticatedAttachment(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	})
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			connection, acceptErr := listener.AcceptUnix()
			if acceptErr != nil {
				return
			}
			_ = connection.Close()
		}
	}()
	client, err := NewRuntimeClient(socketPath, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Brief(context.Background()); err == nil {
		t.Fatal("client accepted an attachment that never proved its relay identity")
	}
}

func TestRuntimeClientCallRejectsUnpinnedMountAuthority(t *testing.T) {
	mountDirectory := mountedRuntimeTestDirectory(t)
	client := &RuntimeClient{
		socketPath:      filepath.Join(mountDirectory, "attachment-0123456789abcdef0123456789abcdef.sock"),
		mountDirectory:  mountDirectory,
		mountTargetName: "attachment-0123456789abcdef0123456789abcdef.sock",
		timeout:         time.Second,
	}
	_, err := client.call(context.Background(), runtimeRequest{Version: runtimeProtocolVersion, Kind: "brief"})
	if err == nil {
		t.Fatal("client dialed a protected mount it never pinned")
	}
	if !strings.Contains(err.Error(), "call runtime attachment") {
		t.Fatalf("client.call(unpinned mount) error = %v", err)
	}
}

func TestRuntimeClientCallRejectsUnusableCallContext(t *testing.T) {
	client := &RuntimeClient{socketPath: "/tmp/absent.sock", timeout: time.Second}
	//lint:ignore SA1012 This boundary test proves nil cannot reach an attachment call.
	if _, err := client.call(nil, runtimeRequest{}); err == nil {
		t.Fatal("client accepted a nil call context")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.call(cancelled, runtimeRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("client.call(cancelled) error = %v", err)
	}
	if _, err := (*RuntimeClient)(nil).call(context.Background(), runtimeRequest{}); err == nil {
		t.Fatal("nil client served a call")
	}
}
