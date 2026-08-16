//go:build darwin || linux

package git

import (
	"crypto/sha256"
	"fmt"
	"os"
	"syscall"
)

func commonDirectoryIdentity(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect git common directory identity: %w", errFilesystemInfrastructure)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("inspect git common directory identity: unsupported file identity: %w", errFilesystemInfrastructure)
	}
	identity := fmt.Sprintf("%s\x00%d\x00%d", path, stat.Dev, stat.Ino)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity))), nil
}
