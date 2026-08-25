package git

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path"
	"sort"
	"strings"
)

func materializeIntegrationWorktree(
	worktreePath string,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) error {
	root, err := os.OpenRoot(worktreePath)
	if err != nil {
		return errors.New("apply integration candidate: materialization root is unavailable")
	}
	defer root.Close()
	matches, err := integrationRootMatchesSnapshot(root, expected)
	if err != nil || !matches {
		return errors.New("apply integration candidate: materialization worktree differs")
	}
	removed := make([]string, 0)
	for name := range expected {
		if _, retained := resulting[name]; !retained {
			removed = append(removed, name)
		}
	}
	sort.Slice(removed, func(left, right int) bool { return len(removed[left]) > len(removed[right]) })
	for _, name := range removed {
		if err := removeMaterializationEntry(root, name); err != nil {
			return err
		}
	}
	if err := removeBlockingMaterializationDirectories(root, expected, resulting); err != nil {
		return err
	}
	written := make([]string, 0)
	for name, result := range resulting {
		if previous, exists := expected[name]; exists && previous.mode == result.mode &&
			previous.objectID == result.objectID {
			continue
		}
		written = append(written, name)
	}
	sort.Strings(written)
	for _, name := range written {
		if err := writeMaterializationEntry(root, name, resulting[name]); err != nil {
			return err
		}
	}
	matches, err = integrationRootMatchesSnapshot(root, resulting)
	if err != nil || !matches {
		return errors.New("apply integration candidate: materialized worktree is unverified")
	}
	return nil
}

func removeMaterializationEntry(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil || info.IsDir() || info.Mode()&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
		return errors.New("apply integration candidate: materialization removal target is invalid")
	}
	if err := root.Remove(name); err != nil {
		return errors.New("apply integration candidate: materialization entry could not be removed")
	}
	return syncMaterializationDirectory(root, path.Dir(name))
}

func removeBlockingMaterializationDirectories(
	root *os.Root,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) error {
	directories := make([]string, 0)
	for resultPath := range resulting {
		prefix := resultPath + "/"
		for expectedPath := range expected {
			if strings.HasPrefix(expectedPath, prefix) {
				directories = append(directories, resultPath)
				break
			}
		}
	}
	sort.Slice(directories, func(left, right int) bool { return len(directories[left]) > len(directories[right]) })
	for _, directory := range directories {
		info, err := root.Lstat(directory)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || root.Remove(directory) != nil {
			return errors.New("apply integration candidate: materialization directory transition is invalid")
		}
	}
	return nil
}

func writeMaterializationEntry(root *os.Root, name string, entry integrationTreeEntry) error {
	if err := ensureMaterializationParents(root, path.Dir(name)); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(name + "\x00" + entry.objectID))
	temporary := path.Join(path.Dir(name), ".comis-materialize-"+hex.EncodeToString(digest[:12]))
	if _, err := root.Lstat(temporary); err == nil || !errors.Is(err, os.ErrNotExist) {
		return errors.New("apply integration candidate: materialization temporary is ambiguous")
	}
	switch entry.mode {
	case "100644", "100755":
		mode := os.FileMode(0o600)
		if entry.mode == "100755" {
			mode = 0o700
		}
		file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return errors.New("apply integration candidate: materialization temporary is unavailable")
		}
		_, writeErr := file.Write(entry.contents)
		chmodErr := file.Chmod(mode)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || chmodErr != nil || closeErr != nil {
			_ = root.Remove(temporary)
			return errors.New("apply integration candidate: materialization file could not be published")
		}
	case "120000":
		if bytes.IndexByte(entry.contents, 0) >= 0 || root.Symlink(string(entry.contents), temporary) != nil {
			return errors.New("apply integration candidate: materialization symlink is invalid")
		}
	default:
		return errors.New("apply integration candidate: materialization mode is invalid")
	}
	if err := root.Rename(temporary, name); err != nil {
		_ = root.Remove(temporary)
		return errors.New("apply integration candidate: materialization entry could not be published")
	}
	return syncMaterializationDirectory(root, path.Dir(name))
}

func ensureMaterializationParents(root *os.Root, directory string) error {
	if directory == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(directory, "/") {
		if current == "" {
			current = component
		} else {
			current += "/" + component
		}
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, 0o700); err != nil {
				return errors.New("apply integration candidate: materialization directory could not be created")
			}
			info, err = root.Lstat(current)
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("apply integration candidate: materialization parent is unsafe")
		}
	}
	return nil
}

func syncMaterializationDirectory(root *os.Root, directory string) error {
	file, err := root.Open(directory)
	if err != nil {
		return errors.New("apply integration candidate: materialization directory is unavailable")
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("apply integration candidate: materialization directory could not be synchronized")
	}
	return nil
}
