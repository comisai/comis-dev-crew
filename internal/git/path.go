package git

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

const maximumConfiguredPathBytes = 4096

func validateCanonicalDirectory(path string) error {
	if err := validateCanonicalPathText(path); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return errors.New("configured directory is unavailable")
	}
	if resolved != path {
		return errors.New("configured directory contains a symbolic link")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return errors.New("configured directory cannot be inspected")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("configured path is not a non-symlink directory")
	}
	return nil
}

func validateGitExecutable(path string) error {
	if err := validateCanonicalPathText(path); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("git executable is unavailable or noncanonical")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return errors.New("git executable is not a regular executable")
	}
	return nil
}

func validateCanonicalPathText(path string) error {
	if path == "" || len([]byte(path)) > maximumConfiguredPathBytes {
		return errors.New("configured path is empty or too long")
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("configured path must be absolute and canonical")
	}
	if strings.IndexFunc(path, unicode.IsControl) >= 0 {
		return errors.New("configured path contains a control character")
	}
	return nil
}

func containedByAny(path string, roots []string) bool {
	for _, root := range roots {
		if pathWithin(root, path, false) {
			return true
		}
	}
	return false
}

func pathWithin(root, path string, strict bool) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return false
	}
	return !strict || relative != "."
}

func pathsOverlap(first, second string) bool {
	return pathWithin(first, second, false) || pathWithin(second, first, false)
}

func validatePrimaryMarker(primary string) error {
	marker := filepath.Join(primary, ".git")
	info, err := os.Lstat(marker)
	if err != nil {
		return errors.New("primary checkout has no git directory")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("configured checkout is not the primary git checkout")
	}
	return nil
}

func validateWorktreeMarker(path string) error {
	marker := filepath.Join(path, ".git")
	info, err := os.Lstat(marker)
	if err != nil {
		return errors.New("task path has no git worktree marker")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("task path is not a linked git worktree")
	}
	return nil
}

func safePathError(operation string, cause error) error {
	return fmt.Errorf("%s: %w", operation, cause)
}
