package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/comisai/comis-dev-crew/internal/command"
	"github.com/comisai/comis-dev-crew/internal/forge"
	"github.com/comisai/comis-dev-crew/internal/localconfig"
	"github.com/comisai/comis-dev-crew/internal/service"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if os.Getenv("DEV_CREW_SSH_TRANSPORT") == "1" {
		os.Exit(forge.RunSSHTransport(ctx, os.Args[1:], os.Stdout, os.Stderr, forge.SSHTransportConfig{
			Executable: os.Getenv("DEV_CREW_SSH_EXECUTABLE"), KeyFile: os.Getenv("DEV_CREW_SSH_KEY_FILE"),
			KnownHostsFile: os.Getenv("DEV_CREW_SSH_KNOWN_HOSTS_FILE"), ExpectedHost: os.Getenv("DEV_CREW_SSH_HOST"),
			RemotePath: os.Getenv("DEV_CREW_SSH_REMOTE_PATH"), GitProtocol: os.Getenv("GIT_PROTOCOL"),
		}))
	}
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
