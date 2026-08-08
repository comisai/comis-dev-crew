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

	"github.com/comisai/comis-dev-crew/internal/cli"
	"github.com/comisai/comis-dev-crew/internal/command"
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/localconfig"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr, cli.Config{
		DefaultSocketPath: defaultSocketPath(),
		Version:           command.Version,
		NewClient: func(socketPath string) (cli.ReadClient, error) {
			return localapi.NewClient(socketPath, 5*time.Second)
		},
		NewOperationID: newOperationID,
	}))
}

func defaultSocketPath() string {
	configurationRoot, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	paths, err := localconfig.Under(configurationRoot)
	if err != nil {
		return ""
	}
	return paths.Socket
}

func newOperationID() (string, error) {
	var entropy [12]byte
	if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return "", fmt.Errorf("generate read operation ID: %w", err)
	}
	return "read-" + hex.EncodeToString(entropy[:]), nil
}
