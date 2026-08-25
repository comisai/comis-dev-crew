package git

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
)

const (
	maximumIsolatedImportObjects           = 8192
	maximumIsolatedObjectCompressedBytes   = 17 << 20
	maximumIsolatedImportCompressedBytes   = 128 << 20
	maximumIsolatedImportDecompressedBytes = 128 << 20
)

type isolatedObjectImport struct {
	objectID string
	contents []byte
}

func planIsolatedObjectImport(source string) ([]isolatedObjectImport, error) {
	plan := make([]isolatedObjectImport, 0)
	compressedTotal := int64(0)
	decompressedTotal := int64(0)
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return validateIsolatedObjectDirectory(source, path)
		}
		if len(plan) == maximumIsolatedImportObjects {
			return errors.New("isolated object count exceeds its bound")
		}
		objectID, err := isolatedObjectID(source, path, entry)
		if err != nil {
			return err
		}
		contents, size, err := readPlannedLooseObject(path, objectID)
		if err != nil {
			return err
		}
		compressedTotal += int64(len(contents))
		decompressedTotal += size
		if compressedTotal > maximumIsolatedImportCompressedBytes ||
			decompressedTotal > maximumIsolatedImportDecompressedBytes {
			return errors.New("isolated object set exceeds its byte bound")
		}
		plan = append(plan, isolatedObjectImport{objectID: objectID, contents: contents})
		return nil
	})
	if err != nil {
		return nil, errors.New("apply integration candidate: isolated result object set is invalid")
	}
	sort.Slice(plan, func(left, right int) bool { return plan[left].objectID < plan[right].objectID })
	return plan, nil
}

func validateIsolatedObjectDirectory(source, directory string) error {
	relative, err := filepath.Rel(source, directory)
	if err != nil || relative == ".." || filepath.IsAbs(relative) {
		return errors.New("isolated object directory is invalid")
	}
	if relative == "." {
		return nil
	}
	if relative == "info" || relative == "pack" {
		return nil
	}
	if len(relative) != 2 || !lowerHex(relative) {
		return errors.New("isolated object directory is invalid")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("isolated object directory is invalid")
	}
	return nil
}

func readPlannedLooseObject(path, objectID string) ([]byte, int64, error) {
	identity, err := os.Lstat(path)
	if err != nil || !identity.Mode().IsRegular() || identity.Size() < 1 ||
		identity.Size() > maximumIsolatedObjectCompressedBytes {
		return nil, 0, errors.New("isolated object compressed size is invalid")
	}
	file, err := openRegularFile(path)
	if err != nil {
		return nil, 0, err
	}
	contents, readErr := io.ReadAll(io.LimitReader(file, maximumIsolatedObjectCompressedBytes+1))
	opened, statErr := file.Stat()
	pathIdentity, pathErr := os.Lstat(path)
	closeErr := file.Close()
	if readErr != nil || statErr != nil || pathErr != nil || closeErr != nil ||
		len(contents) > maximumIsolatedObjectCompressedBytes || !os.SameFile(identity, opened) ||
		!os.SameFile(opened, pathIdentity) || opened.Size() != int64(len(contents)) {
		return nil, 0, errors.New("isolated object identity changed")
	}
	objectType, size, err := inspectLooseGitObjectBytes(contents, objectID)
	if err != nil || !validBoundedIsolatedObject(objectType, size) {
		return nil, 0, errors.New("isolated object content exceeds its bound")
	}
	return contents, size, nil
}

func validBoundedIsolatedObject(objectType string, size int64) bool {
	if size < 0 {
		return false
	}
	switch objectType {
	case "blob", "tree":
		return size <= maximumIntegrationBlobBytes
	case "commit", "tag":
		return size <= 1<<20
	default:
		return false
	}
}
