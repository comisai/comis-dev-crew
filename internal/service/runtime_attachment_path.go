package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func validateRuntimeAttachmentPreparation(request application.RuntimeAttachmentPreparationRequest) error {
	if domain.ValidateOperationID(request.OperationID) != nil || domain.ValidateTaskHandle(request.TaskHandle) != nil ||
		request.Brief.Validate() != nil || request.BriefRevision != request.Brief.Revision ||
		request.BriefRevisionHash != request.Brief.RevisionHash || !filepath.IsAbs(request.WorkingDirectory) ||
		filepath.Clean(request.WorkingDirectory) != request.WorkingDirectory {
		return errors.New("prepare runtime attachment: task scope is invalid")
	}
	resolved, err := filepath.EvalSymlinks(request.WorkingDirectory)
	if err != nil || resolved != request.WorkingDirectory {
		return errors.New("prepare runtime attachment: workspace is not canonical")
	}
	return nil
}

func ensureOwnedRuntimeRoot(path string) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(os.PathSeparator) || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("create runtime attachment coordinator: runtime root is invalid")
	}
	if err := createRuntimeDirectoryPath(path); err != nil {
		return "", errors.New("create runtime attachment coordinator: runtime root is unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("create runtime attachment coordinator: runtime root is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.New("create runtime attachment coordinator: runtime root is not canonical")
	}
	return path, nil
}

func createRuntimeDirectoryPath(path string) error {
	root := filepath.VolumeName(path) + string(os.PathSeparator)
	current := root
	for _, component := range strings.Split(strings.TrimPrefix(path, root), string(os.PathSeparator)) {
		if component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("runtime directory path contains an unsafe component")
		}
	}
	return nil
}

func ensureTaskRuntimeDirectory(path string) error {
	if err := os.Mkdir(path, 0o700); err != nil && !os.IsExist(err) {
		return errors.New("prepare runtime attachment: task runtime directory is unavailable")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("prepare runtime attachment: task runtime directory is unsafe")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return errors.New("prepare runtime attachment: task runtime directory is not canonical")
	}
	return nil
}
