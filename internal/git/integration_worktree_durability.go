package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func ensureMaterializationParents(
	root *os.Root,
	worktree string,
	directory string,
	boundary materializationBoundary,
) error {
	if directory == "." {
		return nil
	}
	current := ""
	for _, component := range strings.Split(filepath.Clean(directory), string(filepath.Separator)) {
		if current == "" {
			current = component
		} else {
			current = filepath.Join(current, component)
		}
		info, err := root.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(current, 0o700); err != nil {
				return errors.New("apply integration candidate: materialization directory could not be created")
			}
			if err := syncMaterializationDirectory(root, filepath.Dir(current)); err != nil {
				return err
			}
			if err := syncMaterializationDirectory(root, current); err != nil {
				return err
			}
			if boundary != nil && current != worktree {
				relative, relativeErr := filepath.Rel(worktree, current)
				if relativeErr != nil || relative == "." || strings.HasPrefix(relative, "..") {
					return errors.New("apply integration candidate: materialization parent identity is invalid")
				}
				boundary("parent-durable", relative)
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
