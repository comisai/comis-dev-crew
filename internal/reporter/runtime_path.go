package reporter

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"golang.org/x/sys/unix"
)

type runtimePathIdentity struct {
	device     uint64
	inode      uint64
	mode       uint32
	changeSec  int64
	changeNsec int64
}

type pinnedRuntimeMount struct {
	descriptors []int
	identities  []runtimePathIdentity
}

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
	if err := validateRuntimeClientTimeout(timeout); err != nil {
		return nil, err
	}
	pinned, err := pinRuntimeMountDirectory(mountDirectory)
	if err != nil {
		return nil, err
	}
	socketIdentity, err := pinnedRuntimeSocketIdentity(pinned, assignedTargetName)
	if err != nil {
		return nil, closePinnedRuntimeMount(pinned, err)
	}
	mountIdentity := pinned.identities[len(pinned.identities)-1]
	if err := pinned.close(); err != nil {
		return nil, errors.New("create runtime attachment client: protected mount release failed")
	}
	return &RuntimeClient{
		socketPath: socketPath, mountDirectory: mountDirectory, mountTargetName: assignedTargetName,
		mountIdentity: mountIdentity, mountedSocketIdentity: socketIdentity, timeout: timeout,
	}, nil
}

func openRuntimeClient(socketPath string, timeout time.Duration) (*RuntimeClient, error) {
	if err := validateRuntimeClientTimeout(timeout); err != nil {
		return nil, err
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

func validateRuntimeClientTimeout(timeout time.Duration) error {
	if timeout <= 0 || timeout > time.Minute {
		return errors.New("create runtime attachment client: timeout is invalid")
	}
	return nil
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

func pinRuntimeMountDirectory(path string) (*pinnedRuntimeMount, error) {
	if !validRuntimeMountDirectory(path) {
		return nil, errors.New("runtime mounted attachment directory is not canonical")
	}
	flags := runtimePinnedDirectoryOpenFlags() | unix.O_CLOEXEC
	root, err := unix.Open(string(filepath.Separator), flags, 0)
	if err != nil {
		return nil, errors.New("runtime mounted attachment directory identity is unavailable")
	}
	pinned := &pinnedRuntimeMount{descriptors: []int{root}}
	rootIdentity, err := runtimeDescriptorIdentity(root)
	if err != nil {
		return nil, closePinnedRuntimeMount(pinned, errors.New("runtime mounted attachment directory identity is unavailable"))
	}
	pinned.identities = append(pinned.identities, rootIdentity)
	for _, component := range strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator)) {
		descriptor, openErr := unix.Openat(pinned.descriptors[len(pinned.descriptors)-1], component, flags, 0)
		if openErr != nil {
			message := "runtime mounted attachment directory identity is unavailable"
			if errors.Is(openErr, unix.ENOENT) {
				message = "runtime mounted attachment directory does not exist"
			} else if errors.Is(openErr, unix.ELOOP) || errors.Is(openErr, unix.ENOTDIR) {
				message = "runtime mounted attachment directory is not canonical"
			}
			return nil, closePinnedRuntimeMount(pinned, errors.New(message))
		}
		pinned.descriptors = append(pinned.descriptors, descriptor)
		identity, identityErr := runtimeDescriptorIdentity(descriptor)
		if identityErr != nil {
			return nil, closePinnedRuntimeMount(pinned, errors.New("runtime mounted attachment directory identity is unavailable"))
		}
		pinned.identities = append(pinned.identities, identity)
	}
	mount := pinned.identities[len(pinned.identities)-1]
	if mount.mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, closePinnedRuntimeMount(pinned, errors.New("runtime mounted attachment path is not a directory"))
	}
	if !safeRuntimeMountPermissions(os.FileMode(mount.mode & 0o777)) {
		return nil, closePinnedRuntimeMount(pinned, errors.New("runtime mounted attachment directory permissions are unsafe: require owner rwx and no group or other read/write access"))
	}
	return pinned, nil
}

func safeRuntimeMountPermissions(mode os.FileMode) bool {
	permissions := mode.Perm()
	return permissions&0o700 == 0o700 && permissions&0o066 == 0
}

func runtimeDescriptorIdentity(descriptor int) (runtimePathIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(descriptor, &stat); err != nil {
		return runtimePathIdentity{}, err
	}
	return runtimeStatIdentity(stat), nil
}

func runtimeStatIdentity(stat unix.Stat_t) runtimePathIdentity {
	return runtimePathIdentity{
		device: uint64(stat.Dev), inode: stat.Ino, mode: uint32(stat.Mode),
		changeSec: stat.Ctim.Sec, changeNsec: stat.Ctim.Nsec,
	}
}

func (identity runtimePathIdentity) sameNode(other runtimePathIdentity) bool {
	return identity.device == other.device && identity.inode == other.inode && identity.mode == other.mode
}

func pinnedRuntimeSocketIdentity(pinned *pinnedRuntimeMount, targetName string) (runtimePathIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstatat(pinned.descriptors[len(pinned.descriptors)-1], targetName, &stat, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return runtimePathIdentity{}, errors.New("create runtime attachment client: socket does not exist")
		}
		return runtimePathIdentity{}, errors.New("create runtime attachment client: socket identity is unavailable")
	}
	identity := runtimeStatIdentity(stat)
	if identity.mode&unix.S_IFMT != unix.S_IFSOCK {
		return runtimePathIdentity{}, errors.New("create runtime attachment client: target is not a Unix socket")
	}
	if identity.mode&0o777 != 0o600 {
		return runtimePathIdentity{}, errors.New("create runtime attachment client: socket permissions are unsafe: require 0600")
	}
	return identity, nil
}

func (pinned *pinnedRuntimeMount) targetPath(targetName string) (string, error) {
	last := len(pinned.descriptors) - 1
	directory, err := runtimePinnedDirectoryPath(pinned.descriptors[last], pinned.identities[last])
	if err != nil {
		return "", err
	}
	path := filepath.Join(directory, targetName)
	if invalidRuntimeSocketPath(path) {
		return "", errors.New("runtime mounted attachment socket path is invalid")
	}
	return path, nil
}

func (client *RuntimeClient) dialMountedRuntimeSocket() (net.Conn, error) {
	pinned, err := pinRuntimeMountDirectory(client.mountDirectory)
	if err != nil {
		return nil, errors.New("call runtime attachment: protected mount identity changed")
	}
	if !client.mountIdentity.sameNode(pinned.identities[len(pinned.identities)-1]) {
		return nil, closePinnedRuntimeMount(pinned, errors.New("call runtime attachment: protected mount identity changed"))
	}
	if client.afterMountPin != nil {
		client.afterMountPin()
	}
	currentSocket, socketErr := pinnedRuntimeSocketIdentity(pinned, client.mountTargetName)
	if socketErr != nil || !client.mountedSocketIdentity.sameNode(currentSocket) {
		return nil, closePinnedRuntimeMount(pinned, errors.New("call runtime attachment: socket identity changed"))
	}
	dialPath, err := pinned.targetPath(client.mountTargetName)
	if err != nil {
		return nil, closePinnedRuntimeMount(pinned, errors.New("call runtime attachment: socket is unavailable"))
	}
	connection, err := net.DialTimeout("unix", dialPath, client.timeout)
	if err != nil {
		return nil, closePinnedRuntimeMount(pinned, errors.New("call runtime attachment: socket is unavailable"))
	}
	currentSocket, socketErr = pinnedRuntimeSocketIdentity(pinned, client.mountTargetName)
	stable := socketErr == nil && client.mountedSocketIdentity.sameNode(currentSocket) &&
		pinned.unchanged() && pinned.matchesPath(client.mountDirectory)
	closeErr := pinned.close()
	if stable && closeErr == nil {
		return connection, nil
	}
	connectionCloseErr := connection.Close()
	if closeErr != nil || connectionCloseErr != nil {
		return nil, errors.New("call runtime attachment: protected mount release failed")
	}
	return nil, errors.New("call runtime attachment: protected mount identity changed")
}

func (pinned *pinnedRuntimeMount) unchanged() bool {
	changeStart := len(pinned.identities) - 3
	if changeStart < 0 {
		changeStart = 0
	}
	for index, descriptor := range pinned.descriptors {
		current, err := runtimeDescriptorIdentity(descriptor)
		if err != nil || !pinned.identities[index].sameNode(current) {
			return false
		}
		if index >= changeStart &&
			(pinned.identities[index].changeSec != current.changeSec || pinned.identities[index].changeNsec != current.changeNsec) {
			return false
		}
	}
	return true
}

func (pinned *pinnedRuntimeMount) matchesPath(path string) bool {
	current, err := pinRuntimeMountDirectory(path)
	if err != nil {
		return false
	}
	matches := pinned.identities[len(pinned.identities)-1].sameNode(current.identities[len(current.identities)-1])
	closeErr := current.close()
	return matches && closeErr == nil
}

func closePinnedRuntimeMount(pinned *pinnedRuntimeMount, result error) error {
	if pinned != nil && pinned.close() != nil {
		return errors.New("runtime mounted attachment directory release failed")
	}
	return result
}

func (pinned *pinnedRuntimeMount) close() error {
	var resultErr error
	for index := len(pinned.descriptors) - 1; index >= 0; index-- {
		resultErr = errors.Join(resultErr, unix.Close(pinned.descriptors[index]))
	}
	pinned.descriptors = nil
	return resultErr
}
