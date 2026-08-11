package reporter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// NewRuntimeClient validates and pins a service-owned source socket identity.
func NewRuntimeClient(socketPath string, timeout time.Duration) (*RuntimeClient, error) {
	if err := validateRuntimeSourceSocketPath(socketPath); err != nil {
		return nil, err
	}
	return openRuntimeClient(socketPath, timeout)
}

// NewMountedRuntimeClient validates the exact activation-assigned target in
// the fixed protected mount, then pins its owner-only Unix socket identity.
func NewMountedRuntimeClient(socketPath, assignedTargetName string, timeout time.Duration) (*RuntimeClient, error) {
	return newMountedRuntimeClient(socketPath, assignedTargetName, application.RuntimeAttachmentMountDirectory, timeout)
}

func newMountedRuntimeClient(socketPath, assignedTargetName, mountDirectory string, timeout time.Duration) (*RuntimeClient, error) {
	if err := validateMountedRuntimeSocketPath(socketPath, assignedTargetName, mountDirectory); err != nil {
		return nil, err
	}
	mountInfo, err := pinRuntimeMountDirectory(mountDirectory)
	if err != nil {
		return nil, err
	}
	client, err := openRuntimeClient(socketPath, timeout)
	if err != nil {
		return nil, err
	}
	client.mountDirectory = mountDirectory
	client.mountInfo = mountInfo
	return client, nil
}

func openRuntimeClient(socketPath string, timeout time.Duration) (*RuntimeClient, error) {
	if timeout <= 0 || timeout > time.Minute {
		return nil, errors.New("create runtime attachment client: timeout is invalid")
	}
	info, err := os.Lstat(socketPath)
	if os.IsNotExist(err) {
		return nil, errors.New("create runtime attachment client: socket does not exist")
	}
	if err != nil {
		return nil, errors.New("create runtime attachment client: socket identity is unavailable")
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil, errors.New("create runtime attachment client: target is not a Unix socket")
	}
	if info.Mode().Perm() != 0o600 {
		return nil, errors.New("create runtime attachment client: socket permissions are unsafe: require 0600")
	}
	return &RuntimeClient{socketPath: socketPath, socketInfo: info, timeout: timeout}, nil
}

func validateRuntimeSourceSocketPath(path string) error {
	if invalidRuntimeSocketPath(path) || filepath.Base(path) != "attachment.sock" {
		return errors.New("runtime attachment socket path is invalid")
	}
	return nil
}

func validateMountedRuntimeSocketPath(path, assignedTargetName, mountDirectory string) error {
	if invalidRuntimeSocketPath(path) || domain.ValidateAttachmentTargetName(assignedTargetName) != nil ||
		!validRuntimeMountDirectory(mountDirectory) ||
		filepath.Dir(path) != mountDirectory || filepath.Base(path) != assignedTargetName {
		return errors.New("runtime mounted attachment socket path is invalid")
	}
	return nil
}

func invalidRuntimeSocketPath(path string) bool {
	return !filepath.IsAbs(path) || filepath.Clean(path) != path || len([]byte(path)) > maximumRuntimePath ||
		strings.ContainsAny(path, "\x00\r\n")
}

func validRuntimeMountDirectory(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path &&
		!strings.ContainsAny(path, "\x00\r\n")
}

func pinRuntimeMountDirectory(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, errors.New("runtime mounted attachment directory does not exist")
	}
	if err != nil {
		return nil, errors.New("runtime mounted attachment directory identity is unavailable")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("runtime mounted attachment directory is not canonical")
	}
	if !info.IsDir() {
		return nil, errors.New("runtime mounted attachment path is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("runtime mounted attachment directory permissions are unsafe: group or other access is forbidden")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return nil, errors.New("runtime mounted attachment directory is not canonical")
	}
	return info, nil
}
