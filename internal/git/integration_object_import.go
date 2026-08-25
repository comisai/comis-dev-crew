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
	destinationIdentity, err := os.Lstat(destination)
	if err != nil {
		return errors.New("apply integration candidate: isolated object directory is invalid")
	}
	destinationRoot, err := os.OpenRoot(destination)
	if err != nil {
		return errors.New("apply integration candidate: isolated object directory is unavailable")
	}
	openedIdentity, openedErr := destinationRoot.Stat(".")
	if openedErr != nil || !openedIdentity.IsDir() || !os.SameFile(destinationIdentity, openedIdentity) {
		_ = destinationRoot.Close()
		return errors.New("apply integration candidate: isolated object directory identity changed")
	}
	walkErr := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
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
		directory, err := openObjectSubdirectory(destinationRoot, objectID[:2])
		if err != nil {
			return err
		}
		copyErr := copyLooseGitObject(path, directory, objectID[2:], objectID)
		return errors.Join(copyErr, directory.Close())
	})
	syncErr := syncObjectRoot(destinationRoot)
	closeErr := destinationRoot.Close()
	if walkErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("apply integration candidate: isolated result objects could not be imported")
	}
	return nil
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
	defer func() { _ = file.Close() }()
	return validateLooseGitObjectFile(file, objectID)
}

func validateLooseGitObjectFile(file *os.File, objectID string) error {
	decompressed, err := zlib.NewReader(file)
	if err != nil {
		return errors.New("loose object compression is invalid")
	}
	reader := bufio.NewReaderSize(decompressed, 256)
	header, err := reader.ReadString(0)
	fields := strings.Fields(strings.TrimSuffix(header, "\x00"))
	if err != nil || len(header) > 128 || len(fields) != 2 || !validLooseObjectType(fields[0]) {
		_ = decompressed.Close()
		return errors.New("loose object header is invalid")
	}
	size, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || size < 0 {
		_ = decompressed.Close()
		return errors.New("loose object size is invalid")
	}
	digest, err := looseObjectDigest(objectID)
	if err != nil {
		_ = decompressed.Close()
		return err
	}
	_, _ = digest.Write([]byte(header))
	written, copyErr := io.Copy(digest, io.LimitReader(reader, size+1))
	closeErr := decompressed.Close()
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

func openObjectSubdirectory(root *os.Root, name string) (*os.Root, error) {
	if len(name) != 2 || !lowerHex(name) {
		return nil, errors.New("shared object directory identity is invalid")
	}
	if err := root.Mkdir(name, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	identity, err := root.Lstat(name)
	if err != nil || !identity.IsDir() || identity.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("shared object directory is invalid")
	}
	directory, err := root.OpenRoot(name)
	if err != nil {
		return nil, errors.New("shared object directory is unavailable")
	}
	opened, err := directory.Stat(".")
	if err != nil || !opened.IsDir() || !os.SameFile(identity, opened) {
		_ = directory.Close()
		return nil, errors.New("shared object directory identity changed")
	}
	return directory, nil
}

func copyLooseGitObject(source string, destination *os.Root, target, objectID string) error {
	input, err := openRegularFile(source)
	if err != nil {
		return err
	}
	if err := validateLooseGitObjectFile(input, objectID); err != nil {
		_ = input.Close()
		return err
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		_ = input.Close()
		return errors.New("isolated object could not be reread")
	}
	if existing, err := destination.Lstat(target); err == nil {
		if !existing.Mode().IsRegular() || !sameRootFileBytes(input, destination, target, objectID) {
			_ = input.Close()
			return errors.New("shared object identity differs")
		}
		return input.Close()
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = input.Close()
		return err
	}
	temporary := target + ".importing"
	if err := discardLooseObjectTemporary(destination, temporary); err != nil {
		_ = input.Close()
		return err
	}
	output, err := destination.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = input.Close()
		return err
	}
	_, copyErr := io.Copy(output, input)
	syncErr := output.Sync()
	closeErr := errors.Join(input.Close(), output.Close())
	if copyErr != nil || syncErr != nil || closeErr != nil || validateRootLooseGitObject(destination, temporary, objectID) != nil {
		_ = destination.Remove(temporary)
		return errors.New("isolated object copy is invalid")
	}
	if err := destination.Link(temporary, target); err != nil {
		if !errors.Is(err, os.ErrExist) || !samePathAndRootFileBytes(source, destination, target, objectID) {
			_ = destination.Remove(temporary)
			return errors.New("isolated object could not be published")
		}
	}
	if err := syncObjectRoot(destination); err != nil {
		_ = destination.Remove(temporary)
		return err
	}
	if err := destination.Remove(temporary); err != nil {
		return errors.New("isolated object temporary could not be removed")
	}
	return syncObjectRoot(destination)
}

func discardLooseObjectTemporary(root *os.Root, path string) error {
	info, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		return errors.New("isolated object temporary is invalid")
	}
	return root.Remove(path)
}

func validateRootLooseGitObject(root *os.Root, path, objectID string) error {
	file, err := openRootRegularFile(root, path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return validateLooseGitObjectFile(file, objectID)
}

func openRootRegularFile(root *os.Root, path string) (*os.File, error) {
	identity, err := root.Lstat(path)
	if err != nil || !identity.Mode().IsRegular() || identity.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("regular file identity is invalid")
	}
	file, err := root.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := file.Stat()
	if err != nil || !os.SameFile(identity, opened) {
		_ = file.Close()
		return nil, errors.New("regular file identity changed")
	}
	return file, nil
}

func samePathAndRootFileBytes(source string, root *os.Root, target, objectID string) bool {
	left, err := openRegularFile(source)
	if err != nil {
		return false
	}
	defer func() { _ = left.Close() }()
	return sameRootFileBytes(left, root, target, objectID)
}

func sameRootFileBytes(leftFile *os.File, root *os.Root, target, objectID string) bool {
	rightFile, err := openRootRegularFile(root, target)
	if err != nil {
		return false
	}
	defer func() { _ = rightFile.Close() }()
	if validateLooseGitObjectFile(rightFile, objectID) != nil {
		return false
	}
	if _, err := leftFile.Seek(0, io.SeekStart); err != nil {
		return false
	}
	if _, err := rightFile.Seek(0, io.SeekStart); err != nil {
		return false
	}
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

func syncObjectRoot(root *os.Root) error {
	directory, err := root.Open(".")
	if err != nil {
		return errors.New("shared object directory is unavailable")
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil || closeErr != nil {
		return errors.New("shared object directory could not be persisted")
	}
	return nil
}
