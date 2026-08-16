package reporter

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMountedRuntimeClientAuthenticatesConnectedRelayBeforeRequest(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	mountDirectory := shortBoundaryDirectory(t)
	socketPath := filepath.Join(mountDirectory, targetName)
	originalPath := filepath.Join(mountDirectory, "original.sock")
	server := listenMountedRuntimeServer(
		t, mountDirectory, targetName, "task-mounted-authentication", bytes.Repeat([]byte{0x31}, runtimeRelaySeedBytes),
	)
	t.Cleanup(func() { _ = server.Close() })
	identity := server.RelayIdentity()
	client, err := newMountedRuntimeClient(socketPath, targetName, mountDirectory, identity, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	maliciousAccepted := make(chan *net.UnixConn, 1)
	var malicious *net.UnixListener
	client.beforeMountedDial = func() {
		if err := os.Rename(socketPath, originalPath); err != nil {
			t.Error(err)
			return
		}
		address, resolveErr := net.ResolveUnixAddr("unix", socketPath)
		if resolveErr != nil {
			t.Error(resolveErr)
			return
		}
		malicious, err = net.ListenUnix("unix", address)
		if err != nil {
			t.Error(err)
			return
		}
		malicious.SetUnlinkOnClose(false)
		if err := os.Chmod(socketPath, 0o600); err != nil {
			t.Error(err)
			return
		}
		go func() {
			connection, acceptErr := malicious.AcceptUnix()
			if acceptErr != nil {
				maliciousAccepted <- nil
				return
			}
			maliciousAccepted <- connection
		}()
	}
	client.afterMountedDial = func() {
		if malicious != nil {
			_ = os.Remove(socketPath)
		}
		if err := os.Rename(originalPath, socketPath); err != nil {
			t.Error(err)
		}
	}
	requestObserved := make(chan bool, 1)
	go func() {
		connection := <-maliciousAccepted
		if connection == nil {
			requestObserved <- false
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		if _, readErr := reader.ReadString('\n'); readErr != nil {
			requestObserved <- false
			return
		}
		_, _ = connection.Write([]byte("invalid-relay-proof\n"))
		_ = connection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		line, _ := reader.ReadString('\n')
		requestObserved <- len(line) != 0
	}()
	if _, err := client.Brief(context.Background()); err == nil {
		t.Fatal("mounted runtime client accepted an unauthenticated transient relay")
	}
	if <-requestObserved {
		t.Fatal("mounted runtime client sent a task request before relay authentication")
	}
	if malicious != nil {
		_ = malicious.Close()
	}
}

func TestMountedRuntimeClientProtectsRequestAcrossAuthenticatedRelayProxy(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-1123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	mountDirectory := filepath.Join(root, "mounted")
	serverDirectory := filepath.Join(root, "server")
	for _, directory := range []string{mountDirectory, serverDirectory} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	socketPath := filepath.Join(mountDirectory, targetName)
	serverPath := filepath.Join(serverDirectory, "attachment.sock")
	server, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: serverPath, Brief: boundaryBrief("task-mounted-encrypted-relay"), Reporter: &Client{},
		RelaySeed: bytes.Repeat([]byte{0x32}, runtimeRelaySeedBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { serveDone <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		<-serveDone
	})
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	malicious, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	malicious.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	observedRequest := make(chan []byte, 1)
	proxyDone := make(chan error, 1)
	go proxyRuntimeConnection(malicious, serverPath, observedRequest, proxyDone)
	client, err := newMountedRuntimeClient(socketPath, targetName, mountDirectory, server.RelayIdentity(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	brief, err := client.Brief(context.Background())
	if err != nil || brief.Content == "" {
		t.Fatalf("Brief() = %#v, %v", brief, err)
	}
	request := <-observedRequest
	if bytes.Contains(request, []byte(`"kind"`)) || bytes.Contains(request, []byte("brief")) {
		t.Fatalf("transient relay observed plaintext request: %q", request)
	}
	if err := <-proxyDone; err != nil {
		t.Fatal(err)
	}
	_ = malicious.Close()
}

func TestMountedRuntimeClientBindsDialToPinnedDirectoryIdentity(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	runDirectory := filepath.Join(root, "run")
	mountDirectory := filepath.Join(runDirectory, "comis", "attachments")
	fakeRunDirectory := filepath.Join(root, "fake-run")
	fakeMountDirectory := filepath.Join(fakeRunDirectory, "comis", "attachments")
	for _, directory := range []string{mountDirectory, fakeMountDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	originalListener, originalConnected := trackedRuntimeSocket(t, filepath.Join(mountDirectory, targetName))
	fakeListener, fakeConnected := trackedRuntimeSocket(t, filepath.Join(fakeMountDirectory, targetName))
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	originalRunDirectory := filepath.Join(root, "original-run")
	var replacementErr error
	client.afterMountPin = func() {
		if err := os.Rename(runDirectory, originalRunDirectory); err != nil {
			replacementErr = err
			return
		}
		if err := os.Rename(fakeRunDirectory, runDirectory); err != nil {
			replacementErr = err
		}
	}
	_, callErr := client.Brief(context.Background())
	if replacementErr != nil {
		t.Fatal(replacementErr)
	}
	if callErr == nil {
		t.Fatal("mounted runtime client accepted a raced mount replacement")
	}
	if err := os.Rename(runDirectory, fakeRunDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(originalRunDirectory, runDirectory); err != nil {
		t.Fatal(err)
	}
	if err := originalListener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fakeListener.Close(); err != nil {
		t.Fatal(err)
	}
	if !<-originalConnected {
		t.Fatal("mounted runtime client did not connect through the pinned directory")
	}
	if <-fakeConnected {
		t.Fatal("mounted runtime client connected through the replacement directory")
	}
}

func TestMountedRuntimeClientRejectsReusedSocketNodeWithDifferentChangeTime(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	mountDirectory := filepath.Join(root, "run", "comis", "attachments")
	if err := os.MkdirAll(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, connected := trackedRuntimeSocket(t, filepath.Join(mountDirectory, targetName))
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.mountedSocketIdentity.changeNsec == 0 {
		client.mountedSocketIdentity.changeNsec = 1
	} else {
		client.mountedSocketIdentity.changeNsec--
	}
	connection, callErr := client.dialMountedRuntimeSocket()
	if connection != nil {
		_ = connection.Close()
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if callErr == nil {
		t.Fatal("mounted runtime client accepted a reused socket node with different change time")
	}
	if <-connected {
		t.Fatal("mounted runtime client connected to a socket with different change time")
	}
}

func TestMountedRuntimeClientRejectsChangedMountInstance(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-2123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	mountDirectory := filepath.Join(root, "run", "comis", "attachments")
	if err := os.MkdirAll(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, connected := trackedRuntimeSocket(t, filepath.Join(mountDirectory, targetName))
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	client.mountIdentity.mountID++
	connection, callErr := client.dialMountedRuntimeSocket()
	if connection != nil {
		_ = connection.Close()
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if callErr == nil {
		t.Fatal("mounted runtime client accepted a changed mount instance")
	}
	if <-connected {
		t.Fatal("mounted runtime client connected through a changed mount instance")
	}
}

func TestMountedRuntimeClientRejectsChangedSocketMountInstance(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-3123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	mountDirectory := filepath.Join(root, "run", "comis", "attachments")
	if err := os.MkdirAll(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, connected := trackedRuntimeSocket(t, filepath.Join(mountDirectory, targetName))
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if client.mountedSocketIdentity.mountID == 0 {
		t.Fatal("mounted runtime client did not capture the socket mount instance")
	}
	client.mountedSocketIdentity.mountID++
	connection, callErr := client.dialMountedRuntimeSocket()
	if connection != nil {
		_ = connection.Close()
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if callErr == nil {
		t.Fatal("mounted runtime client accepted a changed socket mount instance")
	}
	if <-connected {
		t.Fatal("mounted runtime client connected through a changed socket mount instance")
	}
}

func TestMountedRuntimeClientAllowsSiblingDirectoryActivity(t *testing.T) {
	requireRuntimeMountIdentity(t)
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	mountDirectory := filepath.Join(root, "run", "comis", "attachments")
	if err := os.MkdirAll(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, connected := trackedRuntimeSocket(t, filepath.Join(mountDirectory, targetName))
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, boundaryRuntimeRelayIdentity(t), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(mountDirectory, "unrelated-task")
	client.afterMountPin = func() {
		if err := os.Mkdir(sibling, 0o700); err != nil {
			t.Error(err)
		}
	}
	connection, callErr := client.dialMountedRuntimeSocket()
	if connection != nil {
		_ = connection.Close()
	}
	_ = os.Remove(sibling)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if callErr != nil {
		t.Fatalf("dialMountedRuntimeSocket(sibling activity) error = %v", callErr)
	}
	if !<-connected {
		t.Fatal("mounted runtime client did not connect after sibling activity")
	}
}

func requireRuntimeMountIdentity(t *testing.T) {
	t.Helper()
	if !runtimeMountIdentitySupported() {
		t.Skip("platform does not expose mount-instance identity")
	}
}

func trackedRuntimeSocket(t *testing.T, socketPath string) (*net.UnixListener, <-chan bool) {
	t.Helper()
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		t.Fatal(err)
	}
	connected := make(chan bool, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			connected <- false
			return
		}
		_ = connection.Close()
		connected <- true
	}()
	return listener, connected
}

func proxyRuntimeConnection(
	listener *net.UnixListener,
	serverPath string,
	observedRequest chan<- []byte,
	done chan<- error,
) {
	clientConnection, err := listener.AcceptUnix()
	if err != nil {
		done <- err
		return
	}
	defer clientConnection.Close()
	serverConnection, err := net.Dial("unix", serverPath)
	if err != nil {
		done <- err
		return
	}
	defer serverConnection.Close()
	clientReader := bufio.NewReader(clientConnection)
	serverReader := bufio.NewReader(serverConnection)
	auth, err := clientReader.ReadBytes('\n')
	if err == nil {
		_, err = serverConnection.Write(auth)
	}
	var proof []byte
	if err == nil {
		proof, err = serverReader.ReadBytes('\n')
	}
	if err == nil {
		_, err = clientConnection.Write(proof)
	}
	var request []byte
	if err == nil {
		request, err = clientReader.ReadBytes('\n')
	}
	if err == nil {
		observedRequest <- append([]byte(nil), request...)
		_, err = serverConnection.Write(request)
	}
	var response []byte
	if err == nil {
		response, err = serverReader.ReadBytes('\n')
	}
	if err == nil {
		_, err = clientConnection.Write(response)
	}
	done <- err
}

func listenMountedRuntimeServer(
	t *testing.T,
	mountDirectory, targetName, taskHandle string,
	relaySeed []byte,
) *RuntimeServer {
	t.Helper()
	sourcePath := filepath.Join(mountDirectory, "attachment.sock")
	targetPath := filepath.Join(mountDirectory, targetName)
	server, err := ListenRuntime(RuntimeServerConfig{
		SocketPath: sourcePath, Brief: boundaryBrief(taskHandle), Reporter: &Client{}, RelaySeed: relaySeed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(sourcePath, targetPath); err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	if err := server.RelocateSocket(targetPath); err != nil {
		_ = server.Close()
		t.Fatal(err)
	}
	return server
}
