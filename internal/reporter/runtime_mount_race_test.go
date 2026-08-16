package reporter

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMountedRuntimeClientBindsDialToPinnedDirectoryIdentity(t *testing.T) {
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
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, time.Second)
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
	const targetName = "attachment-0123456789abcdef0123456789abcdef.sock"
	root := shortBoundaryDirectory(t)
	mountDirectory := filepath.Join(root, "run", "comis", "attachments")
	if err := os.MkdirAll(mountDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	listener, connected := trackedRuntimeSocket(t, filepath.Join(mountDirectory, targetName))
	client, err := newMountedRuntimeClient(filepath.Join(mountDirectory, targetName), targetName, mountDirectory, time.Second)
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
