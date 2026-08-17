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

	"golang.org/x/sys/unix"
)

func TestRuntimeSourceIdentityStaysDescriptorBound(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.close() })

	identity := pinned.identities[len(pinned.identities)-1]
	if !identity.sameNode(identity) || !identity.sameObject(identity) {
		t.Fatal("pinned directory identity did not match itself")
	}
	changed := identity
	changed.changeNsec++
	if !identity.sameNode(changed) || identity.sameObject(changed) {
		t.Fatal("change time was treated as node identity")
	}
	replaced := identity
	replaced.inode++
	if identity.sameNode(replaced) || identity.sameObject(replaced) {
		t.Fatal("replaced inode retained source identity")
	}

	target, err := pinned.targetPath("attachment.sock")
	if err != nil || filepath.Base(target) != "attachment.sock" || !filepath.IsAbs(target) {
		t.Fatalf("targetPath() = %q, %v", target, err)
	}
	if _, err := pinned.targetPath(strings.Repeat("x", maximumRuntimePath)); err == nil {
		t.Fatal("targetPath accepted an oversized target")
	}
	if !pinned.unchanged() {
		t.Fatal("fresh pinned directory reported changed")
	}
	_ = pinned.matchesPath(root)

	client := &RuntimeClient{mountDirectory: root, timeout: time.Second}
	if connection, err := client.dialMountedRuntimeSocket(); err == nil {
		_ = connection.Close()
		t.Fatal("incomplete mounted client acquired a connection")
	}
}

func TestRuntimeSourceSocketPinningRejectsOtherObjects(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	pinned, err := pinRuntimeMountDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pinned.close() })

	directoryDescriptor := pinned.descriptors[len(pinned.descriptors)-1]
	if _, err := pinnedRuntimeSocketIdentity(pinned, "missing.sock"); err == nil {
		t.Fatal("missing socket acquired identity")
	}
	if err := os.WriteFile(filepath.Join(root, "regular.sock"), []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pinnedRuntimeSocketIdentity(pinned, "regular.sock"); err == nil {
		t.Fatal("regular file acquired socket identity")
	}

	unsafePath := filepath.Join(root, "unsafe.sock")
	unsafeListener := listenRuntimeQuarantineSocket(t, unsafePath)
	t.Cleanup(func() { _ = unsafeListener.Close() })
	if err := os.Chmod(unsafePath, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := pinnedRuntimeSocketIdentity(pinned, "unsafe.sock"); err == nil {
		t.Fatal("unsafe socket permissions acquired identity")
	}

	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	t.Cleanup(func() { _ = listener.Close() })
	identity, err := pinnedRuntimeSocketIdentity(pinned, "attachment.sock")
	if err != nil || identity.mode&unix.S_IFMT != unix.S_IFSOCK {
		t.Fatalf("pinnedRuntimeSocketIdentity() = %#v, %v", identity, err)
	}
	direct, err := runtimePinnedSocketIdentity(directoryDescriptor, "attachment.sock")
	if err != nil || !identity.sameObject(direct) {
		t.Fatalf("runtimePinnedSocketIdentity() = %#v, %v", direct, err)
	}
}

func TestRuntimeDirectoryPublicationWorksForServiceOwnedSource(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	prepared := filepath.Join(root, "prepared")
	if err := os.Mkdir(prepared, 0o700); err != nil {
		t.Fatal(err)
	}
	directory := runtimePathTestDirectoryDescriptor(t, root)
	expected := runtimePathTestIdentity(t, prepared)
	if err := PublishRuntimeDirectory(directory, "prepared", "active", expected, 0o700); err != nil {
		t.Fatalf("PublishRuntimeDirectory() error = %v", err)
	}
	if info, err := os.Lstat(filepath.Join(root, "active")); err != nil || !info.IsDir() {
		t.Fatalf("published directory = %#v, %v", info, err)
	}

	invalid := []func() error{
		func() error { return PublishRuntimePath(-1, "a", "b", expected, 0o600) },
		func() error { return PublishRuntimeDirectory(-1, "a", "b", expected, 0o700) },
		func() error { return ReplaceRuntimePath(-1, "a", "b", expected, expected, 0o600) },
		func() error { return QuarantineRuntimePath(-1, "a", expected, RuntimePathRegular, 0o600) },
	}
	for index, call := range invalid {
		if err := call(); err == nil {
			t.Fatalf("invalid runtime mutation %d succeeded", index)
		}
	}
}

func TestRuntimeSourceRemovalPreservesUnknownReplacement(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := captureRuntimeSocketIdentity(socketPath, info)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeSocket(socketPath, info, identity); err != nil {
		t.Fatalf("removeRuntimeSocket(exact source) error = %v", err)
	}
	_ = listener.Close()
	if _, err := os.Lstat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("removed source socket error = %v", err)
	}

	regularPath := filepath.Join(root, "regular")
	if err := os.WriteFile(regularPath, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	regularInfo, err := os.Lstat(regularPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := captureRuntimeSocketIdentity(regularPath, regularInfo); err == nil {
		t.Fatal("regular file acquired source socket identity")
	}
	if err := removeRuntimeSocket(filepath.Join(root, "missing.sock"), regularInfo, RuntimeSocketIdentity{}); err != nil {
		t.Fatalf("removeRuntimeSocket(missing source) error = %v", err)
	}
	if _, _, err := runtimeSocketFileNode(fakeRuntimeFileInfo{}); err == nil {
		t.Fatal("file info without a stat node acquired identity")
	}
}

type fakeRuntimeFileInfo struct{}

func (fakeRuntimeFileInfo) Name() string       { return "fake" }
func (fakeRuntimeFileInfo) Size() int64        { return 0 }
func (fakeRuntimeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeRuntimeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeRuntimeFileInfo) IsDir() bool        { return false }
func (fakeRuntimeFileInfo) Sys() any           { return nil }

func TestRuntimeSourceClientRejectsUnavailableTargets(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	relayIdentity := boundaryRuntimeRelayIdentity(t)
	if _, err := NewRuntimeClient(filepath.Join(root, "attachment.sock"), relayIdentity, time.Second); err == nil {
		t.Fatal("missing source socket opened")
	}
	regular := filepath.Join(root, "attachment.sock")
	if err := os.WriteFile(regular, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRuntimeClient(regular, relayIdentity, time.Second); err == nil {
		t.Fatal("regular source target opened")
	}
	if _, err := openRuntimeClient(regular, relayIdentity, 0); err == nil {
		t.Fatal("zero client timeout opened")
	}
	if _, err := openRuntimeClient(regular, "invalid", time.Second); err == nil {
		t.Fatal("invalid relay identity opened")
	}
	if !errors.Is(closePinnedRuntimeMount(nil, net.ErrClosed), net.ErrClosed) {
		t.Fatal("nil pinned mount changed the original error")
	}
}

func TestRuntimeServerConnectionRejectsClosedAndUnauthenticatedPeers(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "connection-boundary.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	t.Cleanup(func() { _ = listener.Close() })
	connect := func(t *testing.T) (*net.UnixConn, *net.UnixConn) {
		t.Helper()
		client, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: socketPath, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		server, err := listener.AcceptUnix()
		if err != nil {
			_ = client.Close()
			t.Fatal(err)
		}
		return client, server
	}
	client, connection := connect(t)
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&RuntimeServer{}).serveConnection(context.Background(), connection); err == nil {
		t.Fatal("closed runtime connection was served")
	}
	_ = client.Close()

	client, connection = connect(t)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := (&RuntimeServer{}).serveConnection(context.Background(), connection); err == nil {
		t.Fatal("unauthenticated runtime connection was served")
	}
}

func TestRuntimeSourceReleaseTracksRelocatedSocketIdentity(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	originalPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, originalPath)
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(originalPath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := captureRuntimeSocketIdentity(originalPath, info)
	if err != nil {
		t.Fatal(err)
	}
	server := &RuntimeServer{listener: listener, socketPath: originalPath, socketInfo: info, socketIdentity: identity}
	if captured, err := server.SocketIdentity(); err != nil || captured != identity {
		t.Fatalf("SocketIdentity() = %#v, %v", captured, err)
	}
	relocatedPath := filepath.Join(root, "relocated.sock")
	if err := os.Rename(originalPath, relocatedPath); err != nil {
		t.Fatal(err)
	}
	if err := server.RelocateSocket(relocatedPath); err != nil {
		t.Fatalf("RelocateSocket() error = %v", err)
	}
	if server.socketPath != relocatedPath {
		t.Fatalf("relocated path = %q", server.socketPath)
	}
	if err := server.RelocateSocket(filepath.Join(root, "missing.sock")); err == nil {
		t.Fatal("RelocateSocket accepted a missing socket")
	}
	server.closing = true
	if err := server.RelocateSocket(relocatedPath); err == nil {
		t.Fatal("RelocateSocket accepted a closing server")
	}
	server.closing = false
	changed := identity
	changed.Inode++
	server.socketIdentity = changed
	if err := server.RelocateSocket(relocatedPath); err == nil {
		t.Fatal("RelocateSocket accepted a changed socket identity")
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeSocket(relocatedPath, info, identity); err != nil {
		t.Fatalf("removeRuntimeSocket(relocated) error = %v", err)
	}
	if _, err := (*RuntimeServer)(nil).SocketIdentity(); err == nil {
		t.Fatal("SocketIdentity accepted a nil server")
	}
	if err := (*RuntimeServer)(nil).RelocateSocket(relocatedPath); err == nil {
		t.Fatal("RelocateSocket accepted a nil server")
	}
}

func TestRuntimeSourceDirectoryPinningRejectsReplacementPostures(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	valid := filepath.Join(root, "valid")
	if err := os.Mkdir(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	pinned, err := pinRuntimeMountDirectory(valid)
	if err != nil || !pinned.unchanged() {
		t.Fatalf("pinRuntimeMountDirectory() = %#v, %v", pinned, err)
	}
	if err := pinned.close(); err != nil {
		t.Fatal(err)
	}
	if err := pinned.close(); err != nil {
		t.Fatalf("second close error = %v", err)
	}
	missing := filepath.Join(root, "missing")
	if _, err := pinRuntimeMountDirectory(missing); err == nil {
		t.Fatal("missing directory was pinned")
	}
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("regular"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := pinRuntimeMountDirectory(regular); err == nil {
		t.Fatal("regular file was pinned as a directory")
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := pinRuntimeMountDirectory(symlink); err == nil {
		t.Fatal("symlink was pinned as a directory")
	}
	unsafe := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := pinRuntimeMountDirectory(unsafe); err == nil {
		t.Fatal("broad directory permissions were pinned")
	}
	if _, err := pinRuntimeMountDirectory("relative"); err == nil {
		t.Fatal("relative directory was pinned")
	}
	if _, err := runtimeDescriptorIdentity(-1); err == nil {
		t.Fatal("invalid descriptor acquired identity")
	}
	if _, err := runtimeDirectoryDescriptorIdentity(-1, false); err == nil {
		t.Fatal("invalid directory descriptor acquired identity")
	}
	if err := closePinnedRuntimeMount(nil, errors.New("original")); err == nil || err.Error() != "original" {
		t.Fatalf("closePinnedRuntimeMount(nil) = %v", err)
	}
}

func TestRuntimeSourceRemovalPreservesMismatchedObjects(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := captureRuntimeSocketIdentity(socketPath, info)
	if err != nil {
		t.Fatal(err)
	}
	changed := identity
	changed.Inode++
	if err := removeRuntimeSocket(socketPath, info, changed); err == nil {
		t.Fatal("removeRuntimeSocket removed a mismatched identity")
	}
	if _, err := os.Lstat(socketPath); err != nil {
		t.Fatalf("mismatched socket was not preserved: %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(socketPath); err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeSocket(socketPath, info, identity); err != nil {
		t.Fatalf("missing exact socket error = %v", err)
	}
}

func TestRuntimeSourceIdentityIsCapturedLazily(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	socketPath := filepath.Join(root, "attachment.sock")
	listener := listenRuntimeQuarantineSocket(t, socketPath)
	listener.SetUnlinkOnClose(false)
	info, err := os.Lstat(socketPath)
	if err != nil {
		t.Fatal(err)
	}
	server := &RuntimeServer{listener: listener, socketPath: socketPath, socketInfo: info}
	identity, err := server.SocketIdentity()
	if err != nil || !identity.Valid() || server.socketIdentity != identity {
		t.Fatalf("SocketIdentity() = %#v, %v", identity, err)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := removeRuntimeSocket(socketPath, info, identity); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeIsolationReconciliationKeepsOriginalAuthority(t *testing.T) {
	root := boundaryRuntimeDirectory(t)
	directory := runtimePathTestDirectoryDescriptor(t, root)
	t.Cleanup(func() { _ = unix.Close(directory) })
	expected := RuntimeSocketIdentity{Device: 1, Inode: 2, ChangeSec: 3}

	makeIsolation := func(t *testing.T, name string) int {
		t.Helper()
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			t.Fatal(err)
		}
		descriptor, err := unix.Openat(directory, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		return descriptor
	}

	originalName := "original"
	if err := os.WriteFile(filepath.Join(root, originalName), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	isolation := makeIsolation(t, "isolation-original")
	if reconciled, err := reconcileIsolatedRuntimePath(
		directory, isolation, "isolation-original", originalName, expected, RuntimePathRegular, 0o600,
	); err != nil || reconciled {
		t.Fatalf("reconcileIsolatedRuntimePath(original) = %t, %v", reconciled, err)
	}

	isolation = makeIsolation(t, "isolation-absent")
	if reconciled, err := reconcileIsolatedRuntimePath(
		directory, isolation, "isolation-absent", "absent", expected, RuntimePathRegular, 0o600,
	); err != nil || !reconciled {
		t.Fatalf("reconcileIsolatedRuntimePath(absent) = %t, %v", reconciled, err)
	}

	isolation = makeIsolation(t, "isolation-mismatch")
	if err := os.WriteFile(filepath.Join(root, "isolation-mismatch", runtimePathIsolationTarget), []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	if reconciled, err := reconcileIsolatedRuntimePath(
		directory, isolation, "isolation-mismatch", "missing", expected, RuntimePathRegular, 0o600,
	); err == nil || !reconciled {
		t.Fatalf("reconcileIsolatedRuntimePath(mismatch) = %t, %v", reconciled, err)
	}
}

func TestRuntimeSourceHelpersRejectIncompleteLifecycleAuthority(t *testing.T) {
	if _, err := runtimeSocketStatIdentity(unix.Stat_t{}); err == nil {
		t.Fatal("runtimeSocketStatIdentity accepted an empty identity")
	}
	missing := filepath.Join(t.TempDir(), "missing", "attachment.sock")
	if _, err := captureRuntimeSocketIdentity(missing, fakeRuntimeFileInfo{}); err == nil {
		t.Fatal("captureRuntimeSocketIdentity accepted a missing parent")
	}
	server := &RuntimeServer{socketPath: missing, socketInfo: fakeRuntimeFileInfo{}}
	if _, err := server.SocketIdentity(); err == nil {
		t.Fatal("SocketIdentity accepted unavailable lazy identity authority")
	}
	server.initializeLifecycle()
	server.acceptWaitGroup.Add(1)
	if server.finishAccept(nil) {
		t.Fatal("finishAccept accepted a nil connection")
	}
	if err := validateRuntimeLaunchConfig(RuntimeServerConfig{
		AttentionResponses: boundaryAttentionReceiver{},
	}); err == nil {
		t.Fatal("validateRuntimeLaunchConfig accepted incomplete attention authority")
	}
}
