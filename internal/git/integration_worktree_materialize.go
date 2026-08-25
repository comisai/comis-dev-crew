package git

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type materializationBoundary func(string, string)

func materializeIntegrationWorktree(
	worktreePath string,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) error {
	return materializeIntegrationWorktreeAtBoundary(worktreePath, expected, resulting, nil)
}

func materializeIntegrationWorktreeAtBoundary(
	worktreePath string,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
	boundary materializationBoundary,
) (returnErr error) {
	if err := validateIntegrationMaterializationTopology(expected, resulting); err != nil {
		return err
	}
	parent := filepath.Dir(worktreePath)
	worktree := filepath.Base(worktreePath)
	if worktree == "." || worktree == string(filepath.Separator) || strings.ContainsAny(worktree, `/\`) {
		return errors.New("apply integration candidate: materialization root identity is invalid")
	}
	recoveryParent := ".comis-integration-materialization"
	recovery := filepath.Join(recoveryParent, integrationMaterializationRecoveryIdentity(worktreePath, expected, resulting))
	common, err := os.OpenRoot(parent)
	if err != nil {
		return errors.New("apply integration candidate: materialization root is unavailable")
	}
	defer func() { returnErr = errors.Join(returnErr, common.Close()) }()
	found, err := materializationRecoveryExists(common, recovery)
	if err != nil {
		return err
	}
	if !found {
		matches, matchErr := integrationWorktreeMatchesSnapshot(worktreePath, expected)
		if matchErr != nil || !matches {
			return errors.New("apply integration candidate: materialization worktree differs")
		}
		if err := createMaterializationRecovery(common, recoveryParent, recovery); err != nil {
			return err
		}
	}
	changed := changedMaterializationPaths(expected, resulting)
	for _, name := range changed {
		result, exists := resulting[name]
		if exists {
			if err := stageMaterializationEntry(common, recovery, name, result); err != nil {
				return err
			}
		}
	}
	for _, name := range changed {
		if err := publishMaterializationEntry(common, worktree, recovery, name,
			expected, resulting, boundary); err != nil {
			return err
		}
	}
	matches, err := integrationWorktreeMatchesSnapshot(worktreePath, resulting)
	if err != nil || !matches {
		return errors.New("apply integration candidate: materialized worktree is unverified")
	}
	if boundary != nil {
		boundary("before-recovery-retirement", "")
	}
	if err := retireMaterializationRecovery(common, recovery, changed); err != nil {
		return err
	}
	return nil
}

func integrationMaterializationRecoveryIdentity(
	worktreePath string,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) string {
	names := make([]string, 0, len(expected)+len(resulting))
	seen := make(map[string]struct{}, len(expected)+len(resulting))
	for name := range expected {
		seen[name] = struct{}{}
		names = append(names, name)
	}
	for name := range resulting {
		if _, exists := seen[name]; !exists {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	hash := sha256.New()
	hash.Write([]byte(worktreePath))
	for _, name := range names {
		hash.Write([]byte{0})
		hash.Write([]byte(name))
		for _, snapshot := range []integrationTreeSnapshot{expected, resulting} {
			entry, exists := snapshot[name]
			if !exists {
				hash.Write([]byte{0})
				continue
			}
			hash.Write([]byte{1})
			hash.Write([]byte(entry.mode))
			hash.Write([]byte(entry.objectID))
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func materializationRecoveryExists(root *os.Root, recovery string) (bool, error) {
	info, err := root.Lstat(recovery)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return false, errors.New("apply integration candidate: materialization recovery is invalid")
	}
	return true, nil
}

func integrationMaterializationRecoveryStatus(
	worktreePath string,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) (bool, error) {
	root, err := os.OpenRoot(filepath.Dir(worktreePath))
	if err != nil {
		return false, errors.New("apply integration candidate: materialization recovery root is unavailable")
	}
	recovery := filepath.Join(".comis-integration-materialization",
		integrationMaterializationRecoveryIdentity(worktreePath, expected, resulting))
	found, err := materializationRecoveryExists(root, recovery)
	closeErr := root.Close()
	if err != nil || closeErr != nil {
		return false, errors.New("apply integration candidate: materialization recovery state is unavailable")
	}
	return found, nil
}

func createMaterializationRecovery(root *os.Root, parent string, recovery string) error {
	if info, err := root.Lstat(parent); errors.Is(err, os.ErrNotExist) {
		if err := root.Mkdir(parent, 0o700); err != nil {
			return errors.New("apply integration candidate: materialization recovery is unavailable")
		}
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 {
		return errors.New("apply integration candidate: materialization recovery is invalid")
	}
	if err := root.Mkdir(recovery, 0o700); err != nil {
		return errors.New("apply integration candidate: materialization recovery is unavailable")
	}
	if err := syncMaterializationDirectory(root, parent); err != nil {
		return err
	}
	return syncMaterializationDirectory(root, ".")
}

func changedMaterializationPaths(
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
) []string {
	changed := make([]string, 0)
	for name, previous := range expected {
		result, retained := resulting[name]
		if !retained || previous.mode != result.mode || previous.objectID != result.objectID {
			changed = append(changed, name)
		}
	}
	for name := range resulting {
		if _, exists := expected[name]; !exists {
			changed = append(changed, name)
		}
	}
	sort.Strings(changed)
	return changed
}

func materializationEvidenceName(kind string, name string) string {
	digest := sha256.Sum256([]byte(name))
	return kind + "-" + hex.EncodeToString(digest[:])
}

func stageMaterializationEntry(
	root *os.Root,
	recovery string,
	name string,
	entry integrationTreeEntry,
) error {
	stage := filepath.Join(recovery, materializationEvidenceName("stage", name))
	if err := discardMaterializationEvidenceTemporary(root, stage+".pending"); err != nil {
		return err
	}
	if found, matches, err := materializationEntryState(root, stage, entry); err != nil {
		return err
	} else if found {
		if !matches {
			return errors.New("apply integration candidate: materialization stage differs")
		}
		return nil
	}
	if err := writeMaterializationEvidence(root, stage, entry); err != nil {
		return err
	}
	return syncMaterializationDirectory(root, recovery)
}

func publishMaterializationEntry(
	root *os.Root,
	worktree string,
	recovery string,
	name string,
	expected integrationTreeSnapshot,
	resulting integrationTreeSnapshot,
	boundary materializationBoundary,
) error {
	target := filepath.Join(worktree, filepath.FromSlash(name))
	capture := filepath.Join(recovery, materializationEvidenceName("capture", name))
	publication := filepath.Join(recovery, materializationEvidenceName("publication", name))
	previous, hadPrevious := expected[name]
	result, hasResult := resulting[name]
	captured, captureMatches, err := materializationEntryState(root, capture, previous)
	if err != nil || captured && (!hadPrevious || !captureMatches) {
		baseErr := errors.New("apply integration candidate: captured materialization entry differs")
		if captured {
			return errors.Join(baseErr, restoreCapturedMaterializationEntry(root, capture, target))
		}
		return baseErr
	}
	targetFound, targetExpected, err := materializationEntryState(root, target, previous)
	if err != nil {
		return err
	}
	targetResult := false
	if targetFound && hasResult {
		_, targetResult, err = materializationEntryState(root, target, result)
		if err != nil {
			return err
		}
	}
	if hadPrevious && !captured {
		if targetExpected {
			if boundary != nil {
				boundary("before-capture", name)
			}
			if err := root.Rename(target, capture); err != nil {
				return errors.New("apply integration candidate: materialization entry could not be captured")
			}
			if err := syncMaterializationDirectory(root, filepath.Dir(target)); err != nil {
				return err
			}
			if err := syncMaterializationDirectory(root, recovery); err != nil {
				return err
			}
			captured, captureMatches, err = materializationEntryState(root, capture, previous)
			if err != nil || !captured || !captureMatches {
				return errors.Join(
					errors.New("apply integration candidate: captured materialization entry differs"),
					restoreCapturedMaterializationEntry(root, capture, target),
				)
			}
			targetFound = false
			targetResult = false
		} else if !targetResult {
			return errors.New("apply integration candidate: materialization target changed before capture")
		}
	}
	if !hadPrevious && targetFound && !targetResult {
		return errors.New("apply integration candidate: materialization addition target is occupied")
	}
	if hasResult && !targetResult {
		if targetFound {
			return errors.New("apply integration candidate: materialization publication target changed")
		}
		if err := ensureMaterializationParents(root, worktree, filepath.Dir(target), boundary); err != nil {
			return err
		}
		if err := discardMaterializationEvidenceTemporary(root, publication+".pending"); err != nil {
			return err
		}
		if found, matches, err := materializationEntryState(root, publication, result); err != nil {
			return err
		} else if found && !matches {
			return errors.New("apply integration candidate: materialization publication evidence differs")
		} else if !found {
			if err := writeMaterializationEvidence(root, publication, result); err != nil {
				return err
			}
			if err := syncMaterializationDirectory(root, recovery); err != nil {
				return err
			}
		}
		if boundary != nil {
			boundary("before-publication", name)
		}
		if err := root.Link(publication, target); err != nil {
			return errors.New("apply integration candidate: materialization publication raced another writer")
		}
		if err := syncMaterializationDirectory(root, filepath.Dir(target)); err != nil {
			return err
		}
		if err := root.Remove(publication); err != nil {
			return errors.New("apply integration candidate: materialization publication evidence could not be retired")
		}
		if err := syncMaterializationDirectory(root, recovery); err != nil {
			return err
		}
	}
	if !hasResult && targetFound {
		return errors.New("apply integration candidate: materialization deletion target changed")
	}
	return nil
}

func restoreCapturedMaterializationEntry(root *os.Root, capture string, target string) error {
	if _, err := root.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	worktree := strings.Split(filepath.Clean(target), string(filepath.Separator))[0]
	if err := ensureMaterializationParents(root, worktree, filepath.Dir(target), nil); err != nil {
		return err
	}
	if err := root.Link(capture, target); err != nil {
		return err
	}
	return syncMaterializationDirectory(root, filepath.Dir(target))
}

func materializationEntryState(
	root *os.Root,
	name string,
	entry integrationTreeEntry,
) (bool, bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, errors.New("apply integration candidate: materialization entry is unavailable")
	}
	switch entry.mode {
	case "100644", "100755":
		if !info.Mode().IsRegular() || (entry.mode == "100755") != (info.Mode().Perm()&0o111 != 0) {
			return true, false, nil
		}
		matches, matchErr := integrationRegularFileMatches(root, name, info, entry.contents)
		return true, matches, matchErr
	case "120000":
		if info.Mode()&os.ModeSymlink == 0 {
			return true, false, nil
		}
		target, readErr := root.Readlink(name)
		return true, readErr == nil && bytes.Equal([]byte(target), entry.contents), readErr
	default:
		return true, false, errors.New("apply integration candidate: materialization mode is invalid")
	}
}

func writeMaterializationEvidence(root *os.Root, name string, entry integrationTreeEntry) error {
	temporary := name + ".pending"
	if err := discardMaterializationEvidenceTemporary(root, temporary); err != nil {
		return err
	}
	if err := writeMaterializationEvidenceTemporary(root, temporary, entry); err != nil {
		return err
	}
	if err := root.Link(temporary, name); err != nil {
		removeErr := root.Remove(temporary)
		return errors.Join(
			errors.New("apply integration candidate: materialization evidence could not be published"), removeErr,
		)
	}
	if err := syncMaterializationDirectory(root, filepath.Dir(name)); err != nil {
		return err
	}
	if err := root.Remove(temporary); err != nil {
		return errors.New("apply integration candidate: materialization evidence temporary could not be retired")
	}
	return syncMaterializationDirectory(root, filepath.Dir(name))
}

func writeMaterializationEvidenceTemporary(root *os.Root, name string, entry integrationTreeEntry) error {
	switch entry.mode {
	case "100644", "100755":
		mode := os.FileMode(0o600)
		if entry.mode == "100755" {
			mode = 0o700
		}
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return errors.New("apply integration candidate: materialization stage is unavailable")
		}
		written, writeErr := io.Copy(file, bytes.NewReader(entry.contents))
		chmodErr := file.Chmod(mode)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || written != int64(len(entry.contents)) || chmodErr != nil || syncErr != nil || closeErr != nil {
			return errors.New("apply integration candidate: materialization stage could not be written")
		}
	case "120000":
		if bytes.IndexByte(entry.contents, 0) >= 0 || root.Symlink(string(entry.contents), name) != nil {
			return errors.New("apply integration candidate: materialization symlink stage is invalid")
		}
	default:
		return errors.New("apply integration candidate: materialization mode is invalid")
	}
	return nil
}

func discardMaterializationEvidenceTemporary(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.IsDir() || info.Mode()&(os.ModeDevice|os.ModeNamedPipe|os.ModeSocket) != 0 {
		return errors.New("apply integration candidate: materialization evidence temporary is invalid")
	}
	if err := root.Remove(name); err != nil {
		return errors.New("apply integration candidate: materialization evidence temporary could not be discarded")
	}
	return syncMaterializationDirectory(root, filepath.Dir(name))
}

func retireMaterializationRecovery(
	root *os.Root,
	recovery string,
	changed []string,
) error {
	for _, name := range changed {
		for _, kind := range []string{"stage", "capture", "publication"} {
			evidence := filepath.Join(recovery, materializationEvidenceName(kind, name))
			for _, candidate := range []string{evidence, evidence + ".pending"} {
				if err := root.Remove(candidate); err != nil && !errors.Is(err, os.ErrNotExist) {
					return errors.New("apply integration candidate: materialization recovery could not be retired")
				}
			}
		}
	}
	if err := root.Remove(recovery); err != nil {
		return errors.New("apply integration candidate: materialization recovery could not be retired")
	}
	return syncMaterializationDirectory(root, ".")
}
