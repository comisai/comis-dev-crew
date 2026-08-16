package reporter

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// RuntimeSocketIdentity is the exact filesystem identity of a prepared socket.
type RuntimeSocketIdentity struct {
	Device     uint64
	Inode      uint64
	ChangeSec  int64
	ChangeNsec int64
	BirthSec   int64
	BirthNsec  int64
}

// Valid reports whether an identity can bind later removal authority.
func (identity RuntimeSocketIdentity) Valid() bool {
	return identity.Device != 0 && identity.Inode != 0 && identity.ChangeSec > 0 &&
		identity.ChangeNsec >= 0 && identity.ChangeNsec < int64(time.Second) &&
		((identity.BirthSec == 0 && identity.BirthNsec == 0) ||
			(identity.BirthSec > 0 && identity.BirthNsec >= 0 && identity.BirthNsec < int64(time.Second)))
}

// SocketIdentity returns the immutable identity captured when listening began.
func (server *RuntimeServer) SocketIdentity() (RuntimeSocketIdentity, error) {
	if server == nil || server.socketInfo == nil {
		return RuntimeSocketIdentity{}, errors.New("read runtime attachment identity: server is unavailable")
	}
	server.connectionMu.Lock()
	defer server.connectionMu.Unlock()
	if !server.socketIdentity.Valid() {
		identity, err := captureRuntimeSocketIdentity(server.socketPath, server.socketInfo)
		if err != nil {
			return RuntimeSocketIdentity{}, errors.New("read runtime attachment identity: identity is unavailable")
		}
		server.socketIdentity = identity
	}
	return server.socketIdentity, nil
}

func (server *RuntimeServer) initializeLifecycle() {
	server.lifecycleOnce.Do(func() {
		server.lifecycleContext, server.cancelLifecycle = context.WithCancel(context.Background())
		server.connections = make(map[*net.UnixConn]struct{})
	})
}

func (server *RuntimeServer) beginAccept() bool {
	server.connectionMu.Lock()
	defer server.connectionMu.Unlock()
	if server.closing {
		return false
	}
	server.acceptWaitGroup.Add(1)
	return true
}

func (server *RuntimeServer) finishAccept(connection *net.UnixConn) bool {
	defer server.acceptWaitGroup.Done()
	if connection == nil {
		return false
	}
	server.connectionMu.Lock()
	defer server.connectionMu.Unlock()
	if server.closing {
		if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			server.connectionCloseErr = errors.Join(server.connectionCloseErr, errors.New("close runtime attachment: accepted connection close failed"))
		}
		return false
	}
	server.connections[connection] = struct{}{}
	server.waitGroup.Add(1)
	return true
}

func (server *RuntimeServer) serveTrackedConnection(connection *net.UnixConn) {
	defer func() {
		server.connectionMu.Lock()
		delete(server.connections, connection)
		server.connectionMu.Unlock()
		server.waitGroup.Done()
	}()
	_ = server.serveConnection(server.lifecycleContext, connection)
}

func (server *RuntimeServer) closeRuntimeServer() error {
	server.connectionMu.Lock()
	server.closing = true
	server.cancelLifecycle()
	connections := make([]*net.UnixConn, 0, len(server.connections))
	for connection := range server.connections {
		connections = append(connections, connection)
	}
	server.connectionMu.Unlock()
	var resultErr error
	if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		resultErr = errors.New("close runtime attachment: listener close failed")
	}
	server.acceptWaitGroup.Wait()
	server.connectionMu.Lock()
	connections = connections[:0]
	for connection := range server.connections {
		connections = append(connections, connection)
	}
	server.connectionMu.Unlock()
	for _, connection := range connections {
		if err := connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			resultErr = errors.Join(resultErr, errors.New("close runtime attachment: accepted connection close failed"))
		}
	}
	server.waitGroup.Wait()
	server.connectionMu.Lock()
	resultErr = errors.Join(resultErr, server.connectionCloseErr)
	server.connectionMu.Unlock()
	return errors.Join(resultErr, removeRuntimeSocket(server.socketPath, server.socketInfo, server.socketIdentity))
}

func runtimeSocketFileNode(info os.FileInfo) (uint64, uint64, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil || stat.Dev == 0 || stat.Ino == 0 {
		return 0, 0, errors.New("runtime attachment socket identity is unavailable")
	}
	return uint64(stat.Dev), stat.Ino, nil
}

func runtimeSocketStatIdentity(stat unix.Stat_t) (RuntimeSocketIdentity, error) {
	birthSec, birthNsec := runtimeStatBirthTime(stat)
	identity := RuntimeSocketIdentity{
		Device: uint64(stat.Dev), Inode: stat.Ino,
		ChangeSec: stat.Ctim.Sec, ChangeNsec: stat.Ctim.Nsec,
		BirthSec: birthSec, BirthNsec: birthNsec,
	}
	if !identity.Valid() {
		return RuntimeSocketIdentity{}, errors.New("runtime attachment socket identity is invalid")
	}
	return identity, nil
}

func captureRuntimeSocketIdentity(path string, original os.FileInfo) (RuntimeSocketIdentity, error) {
	pinned, err := pinRuntimeMountDirectory(filepath.Dir(path))
	if err != nil {
		return RuntimeSocketIdentity{}, err
	}
	var stat unix.Stat_t
	if err := unix.Fstatat(pinned.descriptors[len(pinned.descriptors)-1], filepath.Base(path), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return RuntimeSocketIdentity{}, closePinnedRuntimeMount(pinned, err)
	}
	device, inode, err := runtimeSocketFileNode(original)
	if err != nil || device != uint64(stat.Dev) || inode != stat.Ino || stat.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return RuntimeSocketIdentity{}, closePinnedRuntimeMount(pinned, errors.New("runtime attachment socket identity is unavailable"))
	}
	identity, err := runtimeSocketStatIdentity(stat)
	if err != nil {
		return RuntimeSocketIdentity{}, closePinnedRuntimeMount(pinned, err)
	}
	if err := pinned.close(); err != nil {
		return RuntimeSocketIdentity{}, errors.New("runtime attachment socket identity release failed")
	}
	return identity, nil
}

func removeRuntimeSocket(path string, original os.FileInfo, expected RuntimeSocketIdentity) error {
	pinned, err := pinRuntimeMountDirectory(filepath.Dir(path))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return errors.New("close runtime attachment: socket identity is ambiguous; path preserved")
	}
	directoryDescriptor := pinned.descriptors[len(pinned.descriptors)-1]
	if !expected.Valid() {
		var stat unix.Stat_t
		if err := unix.Fstatat(directoryDescriptor, filepath.Base(path), &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			if errors.Is(err, unix.ENOENT) {
				return pinned.close()
			}
			return errors.Join(errors.New("close runtime attachment: socket identity is ambiguous; path preserved"), pinned.close())
		}
		current, identityErr := runtimeSocketStatIdentity(stat)
		device, inode, nodeErr := runtimeSocketFileNode(original)
		if nodeErr == nil && identityErr == nil && device == current.Device && inode == current.Inode {
			expected = current
		}
	}
	removeErr := QuarantineRuntimePath(directoryDescriptor, filepath.Base(path), expected, RuntimePathSocket, 0o600)
	if errors.Is(removeErr, ErrRuntimePathMissing) {
		return pinned.close()
	}
	if removeErr != nil {
		return errors.Join(errors.New("close runtime attachment: socket identity is ambiguous; path preserved"), pinned.close())
	}
	return pinned.close()
}
