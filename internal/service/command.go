package service

import (
	"context"
	"flag"
	"fmt"
	"io"
)

const serviceUsage = `Usage: devcrew-service [--database PATH] [--socket PATH]

Run the sole durable comis-dev-crew service authority.

Options:
  --database PATH  Owner-private SQLite database path
  --socket PATH    Owner-only operator Unix socket path
  --help, -h       Show this help
  --version        Show version
`

// ServiceRunner is the injectable daemon lifecycle used by the command adapter.
type ServiceRunner func(context.Context, Config) error

// CommandConfig supplies host defaults and the version identity.
type CommandConfig struct {
	DefaultDatabasePath string
	DefaultSocketPath   string
	Version             string
	RunService          ServiceRunner
}

// RunCommand parses the strict daemon command surface and returns a process exit code.
func RunCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config CommandConfig) int {
	flags := flag.NewFlagSet("devcrew-service", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	databasePath := config.DefaultDatabasePath
	socketPath := config.DefaultSocketPath
	var help bool
	var version bool
	flags.StringVar(&databasePath, "database", databasePath, "owner-private SQLite database path")
	flags.StringVar(&socketPath, "socket", socketPath, "owner-only operator Unix socket path")
	flags.BoolVar(&help, "help", false, "show help")
	flags.BoolVar(&help, "h", false, "show help")
	flags.BoolVar(&version, "version", false, "show version")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return writeServiceDiagnostic(stderr, "devcrew-service: invalid service arguments\n", 2)
	}
	if help {
		return writeServiceDiagnostic(stdout, serviceUsage, 0)
	}
	if version {
		versionName := config.Version
		if versionName == "" {
			versionName = "dev"
		}
		return writeServiceDiagnostic(stdout, fmt.Sprintf("devcrew-service %s\n", versionName), 0)
	}
	if databasePath == "" || socketPath == "" {
		return writeServiceDiagnostic(stderr, "devcrew-service: service paths are not configured\n", 2)
	}
	runService := config.RunService
	if runService == nil {
		runService = Run
	}
	if err := runService(ctx, Config{DatabasePath: databasePath, SocketPath: socketPath}); err != nil {
		return writeServiceDiagnostic(stderr, "devcrew-service: service stopped with an error\nHint: inspect local configuration and service health\n", 1)
	}
	return 0
}

func writeServiceDiagnostic(destination io.Writer, message string, successCode int) int {
	if destination == nil {
		return 1
	}
	if _, err := io.WriteString(destination, message); err != nil {
		return 1
	}
	return successCode
}
