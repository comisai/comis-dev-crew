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

	"github.com/comisai/comis-dev-crew/internal/command"
	"github.com/comisai/comis-dev-crew/internal/reporter"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	capability, _ := reporter.NewRuntimeClient(os.Getenv("DEV_CREW_ATTACHMENT"), 5*time.Second)
	os.Exit(reporter.RunCommand(ctx, os.Args[1:], os.Stdout, os.Stderr, reporter.CommandConfig{
		Capability: capability, Clock: func() time.Time { return time.Now().UTC() },
		NewLocalReportID: newLocalReportID, WorkingDirectory: canonicalWorkingDirectory,
		Version: command.Version,
	}))
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
