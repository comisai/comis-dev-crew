// Package cli implements the read-only human and script control console.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	ExitSuccess     = 0
	ExitUsage       = 2
	ExitRejected    = 3
	ExitUnavailable = 4
	ExitUncertain   = 5
)

const usage = `Usage: devcrew [--socket PATH] <command>

Read-only commands:
  service status
  doctor [--format table|json]
  status [--format table|json]
  tasks list [--format table|json]
  task show TASK [--format yaml|json]
  task explain TASK [--format text|json]
  task operation OPERATION [--format text|json]

Global options:
  --socket PATH  Owner-only service Unix socket
  --help, -h     Show this help
  --version      Show version
`

// ReadClient is the canonical query client consumed by the CLI adapter.
type ReadClient interface {
	Diagnose(context.Context, string) (application.DiagnosticReport, error)
	Fleet(context.Context, string) (application.FleetSnapshot, error)
	ListTasks(context.Context, string) (application.TaskList, error)
	ShowTask(context.Context, string, string) (application.TaskDetail, error)
	ExplainTask(context.Context, string, string) (application.TaskExplanation, error)
	Operation(context.Context, string, string) (application.OperationView, error)
}

// Config injects host paths, client creation, and operation identity.
type Config struct {
	DefaultSocketPath string
	Version           string
	NewClient         func(string) (ReadClient, error)
	NewOperationID    func() (string, error)
}

type commandKind int

const (
	commandServiceStatus commandKind = iota + 1
	commandDoctor
	commandFleet
	commandListTasks
	commandShowTask
	commandExplainTask
	commandOperation
)

type parsedCommand struct {
	kind       commandKind
	socketPath string
	format     string
	reference  string
}

// Run parses one read-only command, calls the canonical local client, and
// returns a stable process exit code.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer, config Config) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		return writeCLIOutput(stdout, usage, ExitSuccess)
	}
	if len(args) == 1 && args[0] == "--version" {
		version := config.Version
		if version == "" {
			version = "dev"
		}
		return writeCLIOutput(stdout, fmt.Sprintf("devcrew %s\n", version), ExitSuccess)
	}
	command, err := parseCommand(args, config.DefaultSocketPath)
	if err != nil {
		return writeCLIOutput(stderr, "devcrew: invalid command\nHint: run devcrew --help\n", ExitUsage)
	}
	if ctx == nil || config.NewClient == nil || config.NewOperationID == nil || command.socketPath == "" {
		return writeCLIOutput(stderr, "devcrew: local client is unavailable\nHint: inspect CLI configuration\n", ExitUnavailable)
	}
	client, err := config.NewClient(command.socketPath)
	if err != nil || client == nil {
		return writeCLIOutput(stderr, "devcrew: local service is unavailable\nHint: verify the service socket and retry\n", ExitUnavailable)
	}
	operationID, err := config.NewOperationID()
	if err != nil || domain.ValidateOperationID(operationID) != nil {
		return writeCLIOutput(stderr, "devcrew: request identity is unavailable\nHint: retry after checking local entropy\n", ExitUnavailable)
	}
	result, err := execute(ctx, client, operationID, command)
	if err != nil {
		return renderFailure(stderr, err)
	}
	if err := renderResult(stdout, command, result); err != nil {
		return writeCLIOutput(stderr, "devcrew: output is unavailable\nHint: inspect the output destination\n", ExitUnavailable)
	}
	return ExitSuccess
}

func parseCommand(args []string, defaultSocketPath string) (parsedCommand, error) {
	command := parsedCommand{socketPath: defaultSocketPath}
	if len(args) >= 1 && args[0] == "--socket" {
		if len(args) < 3 || args[1] == "" {
			return parsedCommand{}, errors.New("socket path requires a value and command")
		}
		command.socketPath = args[1]
		args = args[2:]
	}
	if len(args) == 0 {
		return parsedCommand{}, errors.New("command is required")
	}
	switch args[0] {
	case "service":
		if len(args) != 2 || args[1] != "status" {
			return parsedCommand{}, errors.New("service status is required")
		}
		command.kind = commandServiceStatus
		command.format = "text"
	case "doctor":
		format, err := parseFormat(args[1:], "table", "table", "json")
		if err != nil {
			return parsedCommand{}, err
		}
		command.kind, command.format = commandDoctor, format
	case "status":
		format, err := parseFormat(args[1:], "table", "table", "json")
		if err != nil {
			return parsedCommand{}, err
		}
		command.kind, command.format = commandFleet, format
	case "tasks":
		if len(args) < 2 || args[1] != "list" {
			return parsedCommand{}, errors.New("tasks list is required")
		}
		format, err := parseFormat(args[2:], "table", "table", "json")
		if err != nil {
			return parsedCommand{}, err
		}
		command.kind, command.format = commandListTasks, format
	case "task":
		return parseTaskCommand(command, args[1:])
	default:
		return parsedCommand{}, errors.New("unknown command")
	}
	return command, nil
}

func parseTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 2 {
		return parsedCommand{}, errors.New("task subcommand and reference are required")
	}
	command.reference = args[1]
	var defaultFormat string
	var allowed []string
	switch args[0] {
	case "show":
		if err := domain.ValidateTaskHandle(command.reference); err != nil {
			return parsedCommand{}, err
		}
		command.kind, defaultFormat, allowed = commandShowTask, "yaml", []string{"yaml", "json"}
	case "explain":
		if err := domain.ValidateTaskHandle(command.reference); err != nil {
			return parsedCommand{}, err
		}
		command.kind, defaultFormat, allowed = commandExplainTask, "text", []string{"text", "json"}
	case "operation":
		if err := domain.ValidateOperationID(command.reference); err != nil {
			return parsedCommand{}, err
		}
		command.kind, defaultFormat, allowed = commandOperation, "text", []string{"text", "json"}
	default:
		return parsedCommand{}, errors.New("unknown task command")
	}
	format, err := parseFormat(args[2:], defaultFormat, allowed...)
	if err != nil {
		return parsedCommand{}, err
	}
	command.format = format
	return command, nil
}

func parseFormat(args []string, defaultFormat string, allowed ...string) (string, error) {
	if len(args) == 0 {
		return defaultFormat, nil
	}
	if len(args) != 2 || args[0] != "--format" {
		return "", errors.New("invalid format arguments")
	}
	for _, candidate := range allowed {
		if args[1] == candidate {
			return candidate, nil
		}
	}
	return "", errors.New("unsupported format")
}

func execute(ctx context.Context, client ReadClient, operationID string, command parsedCommand) (any, error) {
	switch command.kind {
	case commandServiceStatus, commandDoctor:
		return client.Diagnose(ctx, operationID)
	case commandFleet:
		return client.Fleet(ctx, operationID)
	case commandListTasks:
		return client.ListTasks(ctx, operationID)
	case commandShowTask:
		return client.ShowTask(ctx, operationID, command.reference)
	case commandExplainTask:
		return client.ExplainTask(ctx, operationID, command.reference)
	case commandOperation:
		return client.Operation(ctx, operationID, command.reference)
	default:
		return nil, errors.New("unknown parsed command")
	}
}

func renderFailure(stderr io.Writer, err error) int {
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		return writeCLIOutput(stderr, "devcrew: local service request failed\nHint: inspect service health\n", ExitUnavailable)
	}
	exitCode := exitCodeFor(failure.Code)
	message := fmt.Sprintf("devcrew: %s: %s\nHint: %s\n", failure.Code, failure.Message, failure.Hint)
	return writeCLIOutput(stderr, message, exitCode)
}

func exitCodeFor(code domain.ErrorCode) int {
	switch code {
	case domain.ErrorInvalidArgument:
		return ExitUsage
	case domain.ErrorNotFound, domain.ErrorConflict, domain.ErrorUnauthorized, domain.ErrorPrecondition:
		return ExitRejected
	case domain.ErrorDeadlineExceeded, domain.ErrorUnknown:
		return ExitUncertain
	case domain.ErrorUnavailable, domain.ErrorInternal:
		return ExitUnavailable
	default:
		return ExitUnavailable
	}
}

func writeCLIOutput(destination io.Writer, message string, successCode int) int {
	if destination == nil {
		return ExitUnavailable
	}
	if _, err := io.WriteString(destination, message); err != nil {
		return ExitUnavailable
	}
	return successCode
}
