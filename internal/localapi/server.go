package localapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	maximumConnections = 32
	connectionTimeout  = 5 * time.Second
	maximumSocketPath  = 100
)

// Server owns one bounded owner-only Unix-socket endpoint.
type Server struct {
	listener   *net.UnixListener
	socketPath string
	socketInfo os.FileInfo
	caller     CallerClass
	handler    *Handler
	semaphore  chan struct{}
	waitGroup  sync.WaitGroup
	closeOnce  sync.Once
	closeErr   error
	errorMu    sync.Mutex
	serveErr   error
}

// Listen validates the endpoint path and opens it without replacing a live service.
func Listen(socketPath string, caller CallerClass, handler *Handler) (*Server, error) {
	if handler == nil {
		return nil, errors.New("listen local API: handler is required")
	}
	if !caller.valid() {
		return nil, errors.New("listen local API: caller class is invalid")
	}
	if err := prepareSocketPath(socketPath); err != nil {
		return nil, err
	}
	address, err := net.ResolveUnixAddr("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("resolve local API socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return nil, fmt.Errorf("listen on local API socket: %w", err)
	}
	listener.SetUnlinkOnClose(false)
	if err := os.Chmod(socketPath, 0o600); err != nil {
		return nil, errors.Join(
			fmt.Errorf("set local API socket owner-only mode: %w", err),
			listener.Close(),
			os.Remove(socketPath),
		)
	}
	socketInfo, err := os.Lstat(socketPath)
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("capture local API socket identity: %w", err),
			listener.Close(),
			os.Remove(socketPath),
		)
	}
	return &Server{
		listener:   listener,
		socketPath: socketPath,
		socketInfo: socketInfo,
		caller:     caller,
		handler:    handler,
		semaphore:  make(chan struct{}, maximumConnections),
	}, nil
}

// Serve accepts bounded connections until context cancellation or Close.
func (server *Server) Serve(ctx context.Context) (resultErr error) {
	if ctx == nil {
		return errors.New("serve local API: context is required")
	}
	stopCancellation := make(chan struct{})
	cancellationDone := make(chan struct{})
	go func() {
		defer close(cancellationDone)
		select {
		case <-ctx.Done():
			if err := server.Close(); err != nil {
				server.recordServeError(err)
			}
		case <-stopCancellation:
		}
	}()
	defer func() {
		close(stopCancellation)
		<-cancellationDone
		server.waitGroup.Wait()
		resultErr = errors.Join(resultErr, server.firstServeError())
	}()

	for {
		connection, err := server.listener.AcceptUnix()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept local API connection: %w", err)
		}
		select {
		case server.semaphore <- struct{}{}:
			server.waitGroup.Add(1)
			go func() {
				defer server.waitGroup.Done()
				defer func() { <-server.semaphore }()
				if err := server.serveConnection(ctx, connection); err != nil {
					server.recordServeError(err)
				}
			}()
		default:
			if err := connection.Close(); err != nil {
				server.recordServeError(fmt.Errorf("close excess local API connection: %w", err))
			}
		}
	}
}

// Close stops acceptance and removes the exact Unix-socket entry.
func (server *Server) Close() error {
	if server == nil || server.listener == nil {
		return nil
	}
	server.closeOnce.Do(func() {
		var closeErr error
		if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			closeErr = fmt.Errorf("close local API listener: %w", err)
		}
		server.closeErr = errors.Join(closeErr, removeOwnedSocket(server.socketPath, server.socketInfo))
	})
	return server.closeErr
}

func (server *Server) serveConnection(ctx context.Context, connection *net.UnixConn) (resultErr error) {
	defer func() {
		if err := connection.Close(); err != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close local API connection: %w", err))
		}
	}()
	if err := connection.SetDeadline(time.Now().Add(connectionTimeout)); err != nil {
		return fmt.Errorf("set local API connection deadline: %w", err)
	}
	reader := bufio.NewReaderSize(connection, MaxRequestBytes+1)
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) || len(line) > MaxRequestBytes {
		return writeOutcome(connection, rejectedOutcome(unknownRequestID, domain.ErrorInvalidArgument, false, "request exceeds size limit", "send a smaller bounded request", err))
	}
	if err != nil {
		return writeOutcome(connection, rejectedOutcome(unknownRequestID, domain.ErrorInvalidArgument, false, "request must end with a newline", "send one newline-delimited request", err))
	}
	line = line[:len(line)-1]
	outcome := server.handler.handle(ctx, server.caller, line)
	return writeOutcome(connection, outcome)
}

func writeOutcome(connection net.Conn, outcome Outcome) error {
	if err := json.NewEncoder(connection).Encode(outcome); err != nil {
		return fmt.Errorf("write local API outcome: %w", err)
	}
	return nil
}

func (server *Server) recordServeError(err error) {
	server.errorMu.Lock()
	defer server.errorMu.Unlock()
	if server.serveErr == nil {
		server.serveErr = err
	}
}

func (server *Server) firstServeError() error {
	server.errorMu.Lock()
	defer server.errorMu.Unlock()
	return server.serveErr
}

func prepareSocketPath(socketPath string) error {
	if !filepath.IsAbs(socketPath) || filepath.Clean(socketPath) != socketPath {
		return errors.New("listen local API: socket path must be absolute and canonical")
	}
	if len(socketPath) > maximumSocketPath {
		return errors.New("listen local API: socket path is too long")
	}
	parent := filepath.Dir(socketPath)
	root := filepath.VolumeName(parent) + string(os.PathSeparator)
	if parent == root {
		return errors.New("listen local API: socket parent must not be a filesystem root")
	}
	if err := ensureSocketDirectory(parent); err != nil {
		return err
	}
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect local API socket target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return errors.New("listen local API: target must be an absent or stale non-symlink socket")
	}
	connection, dialErr := net.DialTimeout("unix", socketPath, 100*time.Millisecond)
	if dialErr == nil {
		return errors.Join(
			errors.New("listen local API: endpoint is already live"),
			connection.Close(),
		)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("listen local API: socket liveness is uncertain; refusing replacement: %w", dialErr)
	}
	currentInfo, err := os.Lstat(socketPath)
	if err != nil {
		return fmt.Errorf("recheck stale local API socket: %w", err)
	}
	if !os.SameFile(info, currentInfo) {
		return errors.New("listen local API: stale socket identity changed during validation")
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale local API socket: %w", err)
	}
	return nil
}

func removeOwnedSocket(socketPath string, ownedInfo os.FileInfo) error {
	if socketPath == "" || ownedInfo == nil {
		return nil
	}
	currentInfo, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect local API socket during close: %w", err)
	}
	if !os.SameFile(ownedInfo, currentInfo) {
		return errors.New("close local API: socket identity changed; replacement preserved")
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove owned local API socket: %w", err)
	}
	return nil
}

func ensureSocketDirectory(directory string) error {
	root := filepath.VolumeName(directory) + string(os.PathSeparator)
	remainder := strings.TrimPrefix(directory, root)
	current := root
	for _, component := range strings.Split(remainder, string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return fmt.Errorf("create local API directory: %w", err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect local API directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("listen local API: directory path must contain only non-symlink directories")
		}
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("set local API directory owner-only mode: %w", err)
	}
	return nil
}
