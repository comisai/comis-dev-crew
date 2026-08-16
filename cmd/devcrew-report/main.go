package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/command"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

const reporterStartupFailureExitCode = 1

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if reporterMetadataCommand(args) {
		return reporter.RunCommand(ctx, args, stdout, stderr, reporter.CommandConfig{Version: command.Version})
	}
	capability, err := reporter.NewMountedRuntimeClient(
		os.Getenv(application.RuntimeAttachmentPathEnvironment),
		os.Getenv(application.RuntimeAttachmentTargetEnvironment),
		os.Getenv(application.RuntimeAttachmentIdentityEnvironment),
		5*time.Second,
	)
	if err != nil {
		if stderr == nil {
			stderr = io.Discard
		}
		fmt.Fprintf(stderr, "devcrew-report: initialize protected attachment: %v\n", err)
		return reporterStartupFailureExitCode
	}
	return reporter.RunCommand(ctx, args, stdout, stderr, reporter.CommandConfig{
		Capability: capability, Clock: func() time.Time { return time.Now().UTC() },
		NewLocalReportID: newLocalReportID, WorkingDirectory: canonicalWorkingDirectory,
		Version: command.Version,
	})
}

func reporterMetadataCommand(args []string) bool {
	return len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "--version")
}

func canonicalWorkingDirectory() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("read working directory: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(workingDirectory)
	if err != nil || !filepath.IsAbs(canonical) || filepath.Clean(canonical) != canonical {
		return "", errors.New("working directory is not canonical")
	}
	return canonical, nil
}

func newLocalReportID() (string, error) {
	var entropy [12]byte
	if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return "", fmt.Errorf("generate local report ID: %w", err)
	}
	return "report-" + hex.EncodeToString(entropy[:]), nil
}
