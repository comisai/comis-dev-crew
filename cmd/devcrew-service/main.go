package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/comisai/comis-dev-crew/internal/command"
	"github.com/comisai/comis-dev-crew/internal/localconfig"
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
	paths, err := localconfig.Under(configurationRoot)
	if err != nil {
		return "", ""
	}
	return paths.Database, paths.Socket
}
