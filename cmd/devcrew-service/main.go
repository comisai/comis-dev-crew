package main

import (
	"context"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/comisai/comis-dev-crew/internal/command"
	"github.com/comisai/comis-dev-crew/internal/service"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	databasePath, socketPath := defaultPaths()
	os.Exit(service.RunCommand(ctx, os.Args[1:], os.Stdout, os.Stderr, service.CommandConfig{
		DefaultDatabasePath: databasePath,
		DefaultSocketPath:   socketPath,
		Version:             command.Version,
	}))
}

func defaultPaths() (string, string) {
	configurationRoot, err := os.UserConfigDir()
	if err != nil {
		return "", ""
	}
	root := filepath.Join(configurationRoot, "comis-dev-crew")
	return filepath.Join(root, "state", "devcrew.db"), filepath.Join(root, "run", "devcrew.sock")
}
