// Package delivery validates immutable delivery artifacts at the filesystem boundary.
package delivery

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const maximumReportArtifactBytes = int64(1 << 30)

var reportMediaTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]{0,63}/[a-z0-9][a-z0-9.+-]{0,63}$`)

type reportArtifactDependencies struct {
	lstat func(string) (os.FileInfo, error)
	open  func(string) (*os.File, error)
}

// InspectReportArtifact hashes one stable, bounded, non-symlink regular file.
func InspectReportArtifact(
	ctx context.Context,
	path string,
	maximumBytes int64,
	mediaType string,
) (domain.ReportArtifactEvidence, error) {
	return inspectReportArtifact(ctx, path, maximumBytes, mediaType, reportArtifactDependencies{
		lstat: os.Lstat,
		open:  os.Open,
	})
}

func inspectReportArtifact(
	ctx context.Context,
	path string,
	maximumBytes int64,
	mediaType string,
	dependencies reportArtifactDependencies,
) (domain.ReportArtifactEvidence, error) {
	if ctx == nil || dependencies.lstat == nil || dependencies.open == nil || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
		maximumBytes < 1 || maximumBytes > maximumReportArtifactBytes || !reportMediaTypePattern.MatchString(mediaType) {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: input is invalid")
	}
	if err := ctx.Err(); err != nil {
		return domain.ReportArtifactEvidence{}, err
	}
	before, err := dependencies.lstat(path)
	if err != nil {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file is unavailable")
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() < 1 || before.Size() > maximumBytes {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file is not a bounded regular file")
	}
	file, err := dependencies.open(path)
	if err != nil {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file could not be opened")
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	opened, err := file.Stat()
	if err != nil || !sameArtifactFile(before, opened) {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file identity changed during open")
	}
	hasher := sha256.New()
	written, err := io.Copy(hasher, io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file could not be hashed")
	}
	if written != before.Size() || written > maximumBytes {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file size changed during hashing")
	}
	afterOpen, statErr := file.Stat()
	afterPath, lstatErr := dependencies.lstat(path)
	if statErr != nil || lstatErr != nil || !sameArtifactFile(before, afterOpen) || !sameArtifactFile(before, afterPath) {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file changed during hashing")
	}
	if err := ctx.Err(); err != nil {
		return domain.ReportArtifactEvidence{}, err
	}
	if err := file.Close(); err != nil {
		return domain.ReportArtifactEvidence{}, errors.New("inspect report artifact: file close failed")
	}
	closed = true
	return domain.ReportArtifactEvidence{
		ContentHash: fmt.Sprintf("%x", hasher.Sum(nil)), Size: written, MediaType: mediaType,
	}, nil
}

func sameArtifactFile(expected, observed os.FileInfo) bool {
	return expected != nil && observed != nil && os.SameFile(expected, observed) &&
		expected.Mode() == observed.Mode() && expected.Size() == observed.Size() && sameModTime(expected.ModTime(), observed.ModTime())
}

func sameModTime(expected, observed time.Time) bool {
	return expected.Equal(observed)
}
