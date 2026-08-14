package livecampaign

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type recoveryFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Mode   uint32 `json:"mode"`
}

func copyRecoveryTree(source, destination string, excludePlaintextEnvironment bool) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o700)
		}
		if excludePlaintextEnvironment && relative == ".env" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("recovery source contains an unexpected symbolic link")
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0o700)
		}
		if entry.Type().IsRegular() {
			return copyRecoveryFile(path, target)
		}
		if entry.Type()&os.ModeSocket != 0 {
			return nil
		}
		return errors.New("recovery source contains an unexpected special file")
	})
}

func copyRecoveryFile(source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("recovery source file is unavailable or not regular")
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	mode := info.Mode().Perm() & 0o700
	if mode == 0 {
		mode = 0o600
	}
	if err := os.WriteFile(destination, contents, mode); err != nil {
		return err
	}
	return nil
}

func recoveryFiles(root string) ([]recoveryFile, error) {
	files := make([]recoveryFile, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == "recovery-manifest.json" || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.Type().IsRegular() {
			return errors.New("recovery artifact contains an unexpected non-regular entry")
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(contents)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, recoveryFile{
			Path: filepath.ToSlash(relative), SHA256: hex.EncodeToString(digest[:]),
			Bytes: info.Size(), Mode: uint32(info.Mode().Perm()),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(left, right int) bool { return files[left].Path < files[right].Path })
	return files, nil
}

func recoveryDigest(files []recoveryFile) (string, int64, error) {
	hash := sha256.New()
	var bytes int64
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(file.Path)))
		if file.Path == "" || file.Path != clean || filepath.IsAbs(filepath.FromSlash(file.Path)) ||
			file.Path == ".." || strings.HasPrefix(file.Path, "../") ||
			!lowerHexDigestPattern.MatchString(file.SHA256) || file.Bytes < 0 || file.Mode&0o077 != 0 {
			return "", 0, errors.New("recovery file manifest contains an invalid entry")
		}
		if _, exists := seen[file.Path]; exists {
			return "", 0, errors.New("recovery file manifest contains duplicate paths")
		}
		seen[file.Path] = struct{}{}
		if file.Bytes > int64(^uint64(0)>>1)-bytes {
			return "", 0, errors.New("recovery file bytes exceed local bounds")
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%s\x00%d\x00%d\n", file.Path, file.SHA256, file.Bytes, file.Mode)
		bytes += file.Bytes
	}
	return hex.EncodeToString(hash.Sum(nil)), bytes, nil
}

func equalRecoveryFiles(left, right []recoveryFile) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
