// Package cli implements the human and script control console over the typed local client.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/localapi"
)

const (
	ExitSuccess     = 0
	ExitUsage       = 2
	ExitRejected    = 3
	ExitUnavailable = 4
	ExitUncertain   = 5
)

const usage = `Usage: devcrew [--socket PATH] <command>

Commands:
  service status
  doctor [--format table|json]
  status [--format table|json]
  tasks list [--format table|json]
  task show TASK [--format yaml|json]
  task explain TASK [--format text|json]
  task launch-plan TASK [--format json]
  task operation OPERATION [--format text|json]
  task prepare --input FILE|- [--operation OPERATION] [--format json]
  task reconcile TASK --action validate-clean-candidate [--operation OPERATION] [--format json]
  task handback TASK --action validate-developer-work [--operation OPERATION] [--format json]
  task cleanup TASK [--operation OPERATION] [--format json]

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
	GetLaunchPlan(context.Context, string, string) (application.LaunchPlan, error)
	Operation(context.Context, string, string) (application.OperationView, error)
	PrepareTask(context.Context, string, localapi.PrepareTaskInput) (localapi.PrepareTaskResult, error)
	ReconcileTask(context.Context, string, localapi.ReconcileTaskInput) (localapi.TaskMutationResult, error)
	HandbackTask(context.Context, string, localapi.HandbackTaskInput) (localapi.TaskMutationResult, error)
	CleanupTask(context.Context, string, localapi.CleanupTaskInput) (localapi.TaskMutationResult, error)
}

// Config injects host paths, client creation, and operation identity.
type Config struct {
	DefaultSocketPath string
	Version           string
	NewClient         func(string) (ReadClient, error)
	NewOperationID    func() (string, error)
	Stdin             io.Reader
	OpenInput         func(string) (io.ReadCloser, error)
}

type commandKind int

const (
	commandServiceStatus commandKind = iota + 1
	commandDoctor
	commandFleet
	commandListTasks
	commandShowTask
	commandExplainTask
	commandGetLaunchPlan
	commandOperation
	commandPrepareTask
	commandReconcileTask
	commandHandbackTask
	commandCleanupTask
)

type parsedCommand struct {
	kind            commandKind
	socketPath      string
	format          string
	reference       string
	inputPath       string
	operationID     string
	prepareInput    *localapi.PrepareTaskInput
	reconcileAction application.ReconcileTaskAction
	handbackAction  application.HandbackAction
}

// Run parses one canonical command, calls the local client, and
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
	if command.kind == commandPrepareTask {
		input, readErr := readPrepareInput(command.inputPath, config)
		if readErr != nil {
			return writeCLIOutput(stderr, "devcrew: invalid task contract\nHint: provide one strict bounded JSON input\n", ExitUsage)
		}
		command.prepareInput = &input
	}
	if ctx == nil || config.NewClient == nil || config.NewOperationID == nil || command.socketPath == "" {
		return writeCLIOutput(stderr, "devcrew: local client is unavailable\nHint: inspect CLI configuration\n", ExitUnavailable)
	}
	client, err := config.NewClient(command.socketPath)
	if err != nil || client == nil {
		return writeCLIOutput(stderr, "devcrew: local service is unavailable\nHint: verify the service socket and retry\n", ExitUnavailable)
	}
	operationID := command.operationID
	if operationID == "" {
		operationID, err = config.NewOperationID()
	}
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
	if len(args) > 0 && args[0] == "prepare" {
		return parsePrepareTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "reconcile" {
		return parseReconcileTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "handback" {
		return parseHandbackTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "cleanup" {
		return parseCleanupTaskCommand(command, args[1:])
	}
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
	case "launch-plan":
		if err := domain.ValidateTaskHandle(command.reference); err != nil {
			return parsedCommand{}, err
		}
		command.kind, defaultFormat, allowed = commandGetLaunchPlan, "json", []string{"json"}
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

func parseReconcileTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("reconciliation task reference is required")
	}
	command.kind = commandReconcileTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid reconciliation arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--action":
			if value != string(application.ReconcileValidateCleanCandidate) {
				return parsedCommand{}, errors.New("unsupported reconciliation action")
			}
			command.reconcileAction = application.ReconcileTaskAction(value)
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid reconciliation operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("reconciliation format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown reconciliation option")
		}
		args = args[2:]
	}
	if command.reconcileAction == "" {
		return parsedCommand{}, errors.New("reconciliation action is required")
	}
	return command, nil
}

func parseCleanupTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("cleanup task reference is required")
	}
	command.kind = commandCleanupTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid cleanup arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid cleanup operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("cleanup format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown cleanup option")
		}
		args = args[2:]
	}
	return command, nil
}

func parseHandbackTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("handback task reference is required")
	}
	command.kind = commandHandbackTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid handback arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--action":
			if value != string(application.HandbackValidateDeveloperWork) {
				return parsedCommand{}, errors.New("unsupported handback action")
			}
			command.handbackAction = application.HandbackAction(value)
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid handback operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("handback format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown handback option")
		}
		args = args[2:]
	}
	if command.handbackAction == "" {
		return parsedCommand{}, errors.New("handback action is required")
	}
	return command, nil
}

func parsePrepareTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	command.kind = commandPrepareTask
	command.format = "json"
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid prepare arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--input":
			if value == "" {
				return parsedCommand{}, errors.New("prepare input is required")
			}
			command.inputPath = value
		case "--operation":
			if err := domain.ValidateOperationID(value); err != nil {
				return parsedCommand{}, err
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("prepare format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown prepare option")
		}
		args = args[2:]
	}
	if command.inputPath == "" {
		return parsedCommand{}, errors.New("prepare input is required")
	}
	return command, nil
}

func readPrepareInput(path string, config Config) (localapi.PrepareTaskInput, error) {
	var reader io.Reader
	var closer io.Closer
	if path == "-" {
		reader = config.Stdin
	} else {
		if config.OpenInput == nil {
			return localapi.PrepareTaskInput{}, errors.New("input file access is unavailable")
		}
		opened, err := config.OpenInput(path)
		if err != nil {
			return localapi.PrepareTaskInput{}, err
		}
		reader, closer = opened, opened
	}
	if reader == nil {
		return localapi.PrepareTaskInput{}, errors.New("input is unavailable")
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	data, err := io.ReadAll(io.LimitReader(reader, localapi.MaxRequestBytes+1))
	if err != nil || len(data) > localapi.MaxRequestBytes {
		return localapi.PrepareTaskInput{}, errors.New("read bounded task contract")
	}
	return localapi.DecodePrepareTaskInput(data)
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
	case commandGetLaunchPlan:
		return client.GetLaunchPlan(ctx, operationID, command.reference)
	case commandOperation:
		return client.Operation(ctx, operationID, command.reference)
	case commandPrepareTask:
		if command.prepareInput == nil {
			return nil, errors.New("prepare input is unavailable")
		}
		return client.PrepareTask(ctx, operationID, *command.prepareInput)
	case commandReconcileTask:
		return client.ReconcileTask(ctx, operationID, localapi.ReconcileTaskInput{
			TaskHandle: command.reference, Action: command.reconcileAction,
		})
	case commandHandbackTask:
		return client.HandbackTask(ctx, operationID, localapi.HandbackTaskInput{
			TaskHandle: command.reference, Action: command.handbackAction,
		})
	case commandCleanupTask:
		return client.CleanupTask(ctx, operationID, localapi.CleanupTaskInput{TaskHandle: command.reference})
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
