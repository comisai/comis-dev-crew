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
	"github.com/comisai/comis-dev-crew/internal/localapi"
	"github.com/comisai/comis-dev-crew/internal/localconfig"
	"github.com/comisai/comis-dev-crew/internal/mcpadapter"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(mcpadapter.RunCommand(ctx, os.Args[1:], os.Stdout, os.Stderr, mcpadapter.CommandConfig{
		DefaultSocketPath: defaultMCPSocketPath(), Version: command.Version,
		NewClient: func(socketPath string) (mcpadapter.Client, error) {
			return localapi.NewClient(socketPath, 5*time.Second)
		},
		NewOperationID: newReconciliationID,
		Transport:      &mcp.StdioTransport{},
	}))
}

func defaultMCPSocketPath() string {
	configurationRoot, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	paths, err := localconfig.Under(configurationRoot)
	if err != nil {
		return ""
	}
	return paths.MCPSocket
}

func newReconciliationID() (string, error) {
	var entropy [12]byte
	if _, err := io.ReadFull(rand.Reader, entropy[:]); err != nil {
		return "", fmt.Errorf("generate reconciliation operation ID: %w", err)
	}
	return "reconcile-" + hex.EncodeToString(entropy[:]), nil
}
