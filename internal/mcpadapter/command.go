package mcpadapter

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const commandUsage = `Usage: devcrew-mcp [--socket PATH] --service-instance ID

Run the stateless MCP facade over standard input and output.

Options:
  --socket PATH          Owner-only MCP service Unix socket
  --service-instance ID  Exact Comis capability-service instance identity
  --help, -h             Show this help
  --version              Show version
`

// FacadeRunner is the injectable SDK lifecycle used by the command adapter.
type FacadeRunner func(context.Context, *Facade, mcp.Transport) error

// CommandConfig supplies process defaults and stateless composition seams.
type CommandConfig struct {
	DefaultSocketPath        string
	DefaultServiceInstanceID string
	Version                  string
	NewClient                func(string) (Client, error)
	NewOperationID           func() (string, error)
	Transport                mcp.Transport
	RunFacade                FacadeRunner
}

// RunCommand parses the strict MCP process surface and returns an exit code.
func RunCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config CommandConfig) int {
	flags := flag.NewFlagSet("devcrew-mcp", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socketPath := config.DefaultSocketPath
	serviceInstanceID := config.DefaultServiceInstanceID
	var help bool
	var version bool
	flags.StringVar(&socketPath, "socket", socketPath, "owner-only MCP service Unix socket")
	flags.StringVar(&serviceInstanceID, "service-instance", serviceInstanceID, "exact service instance identity")
	flags.BoolVar(&help, "help", false, "show help")
	flags.BoolVar(&help, "h", false, "show help")
	flags.BoolVar(&version, "version", false, "show version")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return writeCommandDiagnostic(stderr, "devcrew-mcp: invalid MCP arguments\n", 2)
	}
	if help {
		return writeCommandDiagnostic(stdout, commandUsage, 0)
	}
	if version {
		versionName := config.Version
		if versionName == "" {
			versionName = "dev"
		}
		return writeCommandDiagnostic(stdout, fmt.Sprintf("devcrew-mcp %s\n", versionName), 0)
	}
	if socketPath == "" || serviceInstanceID == "" {
		return writeCommandDiagnostic(stderr, "devcrew-mcp: MCP configuration is incomplete\nHint: configure the service socket and instance identity\n", 2)
	}
	if ctx == nil || config.NewClient == nil {
		return writeCommandDiagnostic(stderr, "devcrew-mcp: MCP composition is unavailable\nHint: inspect local configuration\n", 1)
	}
	client, err := config.NewClient(socketPath)
	if err != nil || client == nil {
		return writeCommandDiagnostic(stderr, "devcrew-mcp: local service is unavailable\nHint: verify the MCP service socket\n", 1)
	}
	facade, err := New(Config{
		Client: client, ServiceInstanceID: serviceInstanceID, Version: config.Version,
		NewOperationID: config.NewOperationID,
	})
	if err != nil {
		return writeCommandDiagnostic(stderr, "devcrew-mcp: MCP composition is unavailable\nHint: inspect local configuration\n", 1)
	}
	runFacade := config.RunFacade
	if runFacade == nil {
		runFacade = func(runContext context.Context, configured *Facade, transport mcp.Transport) error {
			return configured.Run(runContext, transport)
		}
	}
	if err := runFacade(ctx, facade, config.Transport); err != nil {
		return writeCommandDiagnostic(stderr, "devcrew-mcp: MCP session stopped with an error\nHint: reconnect through the MCP client\n", 1)
	}
	return 0
}

func writeCommandDiagnostic(destination io.Writer, message string, successCode int) int {
	if destination == nil {
		return 1
	}
	if _, err := io.WriteString(destination, message); err != nil {
		return 1
	}
	return successCode
}
