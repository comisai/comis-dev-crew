package git

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

var inplaceMaterializationIdentity = integrationTreeEntry{
	mode: "100644", objectID: "inplace-v1", contents: []byte("inplace-v1\n"),
}

func publishExistingRegularMaterializationEntry(
	root *os.Root,
	target string,
	recovery string,
	name string,
	previous integrationTreeEntry,
	result integrationTreeEntry,
	boundary materializationBoundary,
) error {
	if !regularIntegrationMode(previous.mode) || !regularIntegrationMode(result.mode) {
		return errors.New("apply integration candidate: materialization type transition is unsupported")
	}
	capture := filepath.Join(recovery, materializationEvidenceName("capture", name))
	identity := filepath.Join(recovery, materializationEvidenceName("inplace", name))
	identityFound, identityMatches, err := materializationEntryState(root, identity, inplaceMaterializationIdentity)
	if err != nil || identityFound && !identityMatches {
		return errors.New("apply integration candidate: in-place materialization identity differs")
	}
	captured, captureMatches, err := materializationEntryState(root, capture, previous)
	if err != nil || captured && !captureMatches {
		return errors.New("apply integration candidate: captured materialization entry differs")
	}
	if captured && !identityFound {
		return errors.New("apply integration candidate: displaced materialization entry requires intervention")
	}
	targetFound, targetExpected, err := materializationEntryState(root, target, previous)
	if err != nil {
		return err
	}
	targetResult := false
	if targetFound {
		_, targetResult, err = materializationEntryState(root, target, result)
		if err != nil {
			return err
		}
	}
	if !targetExpected && !targetResult {
		return errors.New("apply integration candidate: in-place materialization target differs")
	}
	if targetResult {
		if !captured || !identityFound {
			return errors.New("apply integration candidate: in-place materialization evidence is incomplete")
		}
		return nil
	}
	if boundary != nil {
		boundary("before-capture", name)
	}
	if _, matches, err := materializationEntryState(root, target, previous); err != nil || !matches {
		return errors.New("apply integration candidate: in-place materialization target changed before capture")
	}
	if !identityFound {
		if err := writeMaterializationEvidence(root, identity, inplaceMaterializationIdentity); err != nil {
			return err
		}
	}
	if !captured {
		if err := writeMaterializationEvidence(root, capture, previous); err != nil {
			return err
		}
	}
	if boundary != nil {
		boundary("before-publication", name)
	}
	if _, matches, err := materializationEntryState(root, target, previous); err != nil || !matches {
		return errors.New("apply integration candidate: in-place materialization target changed before publication")
	}
	return rewriteMaterializationRegularEntry(root, target, result)
}

func rewriteMaterializationRegularEntry(root *os.Root, target string, result integrationTreeEntry) error {
	identity, err := root.Lstat(target)
	if err != nil || !identity.Mode().IsRegular() || identity.Mode()&os.ModeSymlink != 0 {
		return errors.New("apply integration candidate: in-place materialization target is unavailable")
	}
	file, err := root.OpenFile(target, os.O_WRONLY, 0)
	if err != nil {
		return errors.New("apply integration candidate: in-place materialization target is unavailable")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(identity, opened) {
		_ = file.Close()
		return errors.New("apply integration candidate: in-place materialization target identity changed")
	}
	mode := os.FileMode(0o600)
	if result.mode == "100755" {
		mode = 0o700
	}
	truncateErr := file.Truncate(0)
	_, seekErr := file.Seek(0, io.SeekStart)
	written, writeErr := io.Copy(file, bytes.NewReader(result.contents))
	chmodErr := file.Chmod(mode)
	syncErr := file.Sync()
	closeErr := file.Close()
	if truncateErr != nil || seekErr != nil || writeErr != nil || written != int64(len(result.contents)) ||
		chmodErr != nil || syncErr != nil || closeErr != nil {
		return errors.New("apply integration candidate: in-place materialization target write is incomplete")
	}
	_, matches, err := materializationEntryState(root, target, result)
	if err != nil || !matches {
		return errors.New("apply integration candidate: in-place materialization target is unverified")
	}
	return syncMaterializationDirectory(root, filepath.Dir(target))
}
