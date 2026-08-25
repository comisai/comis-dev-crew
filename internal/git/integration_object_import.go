package git

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func importIsolatedGitObjects(source, destination string) error {
	if !filepath.IsAbs(source) || !filepath.IsAbs(destination) || source == destination {
		return errors.New("apply integration candidate: isolated object boundary is invalid")
	}
	if !validObjectDirectory(source) || !validObjectDirectory(destination) {
		return errors.New("apply integration candidate: isolated object directory is invalid")
	}
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		objectID, err := isolatedObjectID(source, path, entry)
		if err != nil {
			return err
		}
		if err := validateLooseGitObject(path, objectID); err != nil {
			return err
		}
		directory := filepath.Join(destination, objectID[:2])
		if err := os.Mkdir(directory, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
		if !validObjectDirectory(directory) {
			return errors.New("shared object directory is invalid")
		}
		return copyLooseGitObject(path, filepath.Join(directory, objectID[2:]), objectID)
	})
	if err != nil {
		return errors.New("apply integration candidate: isolated result objects could not be imported")
	}
	return syncDirectory(destination)
}

func validObjectDirectory(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

func isolatedObjectID(source, path string, entry os.DirEntry) (string, error) {
	info, err := entry.Info()
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("isolated object entry is invalid")
	}
	relative, err := filepath.Rel(source, path)
	if err != nil {
		return "", err
	}
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 2 || len(parts[0]) != 2 || len(parts[1]) != 38 && len(parts[1]) != 62 ||
		!lowerHex(parts[0]+parts[1]) {
		return "", errors.New("isolated object identity is invalid")
	}
	return parts[0] + parts[1], nil
}

func validateLooseGitObject(path, objectID string) error {
	file, err := openRegularFile(path)
	if err != nil {
		return errors.New("loose object is unavailable")
	}
	decompressed, err := zlib.NewReader(file)
	if err != nil {
		_ = file.Close()
		return errors.New("loose object compression is invalid")
	}
	reader := bufio.NewReaderSize(decompressed, 256)
	header, err := reader.ReadString(0)
	fields := strings.Fields(strings.TrimSuffix(header, "\x00"))
	if err != nil || len(header) > 128 || len(fields) != 2 || !validLooseObjectType(fields[0]) {
		_ = decompressed.Close()
		_ = file.Close()
		return errors.New("loose object header is invalid")
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		_ = decompressed.Close()
		_ = file.Close()
		return errors.New("loose object size is invalid")
	}
	digest, err := looseObjectDigest(objectID)
	if err != nil {
		_ = decompressed.Close()
		_ = file.Close()
		return err
	}
	_, _ = digest.Write([]byte(header))
	written, copyErr := io.Copy(digest, io.LimitReader(reader, size+1))
	closeErr := errors.Join(decompressed.Close(), file.Close())
	if copyErr != nil || closeErr != nil || written != size || hex.EncodeToString(digest.Sum(nil)) != objectID {
		return errors.New("loose object content is invalid")
	}
	return nil
}

func validLooseObjectType(value string) bool {
	return value == "blob" || value == "commit" || value == "tree" || value == "tag"
}

func looseObjectDigest(objectID string) (hash.Hash, error) {
	switch len(objectID) {
	case 40:
		return sha1.New(), nil
	case 64:
		return sha256.New(), nil
	default:
		return nil, errors.New("loose object identity is invalid")
	}
}

func openRegularFile(path string) (*os.File, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("regular file identity is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		return nil, errors.New("regular file identity changed")
	}
	return file, nil
}

func copyLooseGitObject(source, target, objectID string) error {
	if existing, err := os.Lstat(target); err == nil {
		if !existing.Mode().IsRegular() || validateLooseGitObject(target, objectID) != nil || !sameFileBytes(source, target) {
			return errors.New("shared object identity differs")
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary := target + ".importing"
	if err := discardLooseObjectTemporary(temporary); err != nil {
		return err
	}
	input, err := openRegularFile(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = input.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := errors.Join(input.Close(), output.Close())
	if copyErr != nil || syncErr != nil || closeErr != nil || validateLooseGitObject(temporary, objectID) != nil {
		_ = os.Remove(temporary)
		return errors.New("isolated object copy is invalid")
	}
	if err := os.Link(temporary, target); err != nil {
		if !errors.Is(err, os.ErrExist) || validateLooseGitObject(target, objectID) != nil || !sameFileBytes(source, target) {
			_ = os.Remove(temporary)
			return errors.New("isolated object could not be published")
		}
	}
	if err := syncDirectory(filepath.Dir(target)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Remove(temporary); err != nil {
		return errors.New("isolated object temporary could not be removed")
	}
	return syncDirectory(filepath.Dir(target))
}

func discardLooseObjectTemporary(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("isolated object temporary is invalid")
	}
	return os.Remove(path)
}

func sameFileBytes(left, right string) bool {
	leftFile, err := openRegularFile(left)
	if err != nil {
		return false
	}
	defer func() { _ = leftFile.Close() }()
	rightFile, err := openRegularFile(right)
	if err != nil {
		return false
	}
	defer func() { _ = rightFile.Close() }()
	leftInfo, leftErr := leftFile.Stat()
	rightInfo, rightErr := rightFile.Stat()
	if leftErr != nil || rightErr != nil || leftInfo.Size() != rightInfo.Size() {
		return false
	}
	leftBuffer := make([]byte, 32*1024)
	rightBuffer := make([]byte, 32*1024)
	for {
		leftCount, leftErr := leftFile.Read(leftBuffer)
		rightCount, rightErr := rightFile.Read(rightBuffer)
		if leftCount != rightCount || !bytes.Equal(leftBuffer[:leftCount], rightBuffer[:rightCount]) {
			return false
		}
		if leftErr == io.EOF && rightErr == io.EOF {
			return true
		}
		if leftErr != nil || rightErr != nil {
			return false
		}
	}
}
