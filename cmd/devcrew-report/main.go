package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/signal"
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
		NewLocalReportID: newLocalReportID, Version: command.Version,
	}))
}

func newLocalReportID() (string, error) {
	var entropy [12]byte
	if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return "", fmt.Errorf("generate local report ID: %w", err)
	}
	return "report-" + hex.EncodeToString(entropy[:]), nil
}
