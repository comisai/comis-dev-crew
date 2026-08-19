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
  workers list [--format table|json]
  task show TASK [--format yaml|json]
  task explain TASK [--format text|json]
  task diff TASK [--stat|--name-only] [--format text|json]
  task launch-plan TASK [--format json]
  task operation OPERATION [--format text|json]
  task prepare --input FILE|- [--operation OPERATION] [--format json]
  task reconcile TASK --action validate-clean-candidate [--operation OPERATION] [--format json]
  task handback TASK --action validate-developer-work [--operation OPERATION] [--format json]
  task pause TASK [--operation OPERATION] [--format json]
  task cancel TASK [--operation OPERATION] [--format json]
  task resume TASK [--operation OPERATION] [--format json]
  task verify TASK [--operation OPERATION] [--format json]
  task promote SCOUT --input FILE|- [--operation OPERATION] [--format json]
  task replace TASK --worker PROFILE [--operation OPERATION] [--format json]
  task steer TASK --instruction TEXT [--operation OPERATION] [--format json]
  task cleanup TASK [--operation OPERATION] [--format json]
  task discard TASK --yes [--operation OPERATION] [--format json]
  repair reconcile [--task TASK] [--format table|json]
  decisions list [--task TASK] [--format table|json]
  decision show TASK DECISION [--format text|json]

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
	ListWorkerProfiles(context.Context, string) (application.WorkerProfileList, error)
	PauseTask(context.Context, string, localapi.PauseTaskInput) (localapi.TaskMutationResult, error)
	CancelTask(context.Context, string, localapi.CancelTaskInput) (localapi.TaskMutationResult, error)
	ResumeTask(context.Context, string, localapi.ResumeTaskInput) (localapi.TaskMutationResult, error)
	VerifyTask(context.Context, string, localapi.VerifyTaskInput) (localapi.TaskMutationResult, error)
	PromoteScout(context.Context, string, localapi.PromoteScoutInput) (localapi.PrepareTaskResult, error)
	ReplaceWorker(context.Context, string, localapi.ReplaceWorkerInput) (localapi.TaskMutationResult, error)
	SteerTask(context.Context, string, localapi.SteerTaskInput) (localapi.TaskMutationResult, error)
	AttestScoutDecisions(context.Context, string, localapi.AttestScoutDecisionsInput) (localapi.TaskMutationResult, error)
	DiscardTask(context.Context, string, localapi.DiscardTaskInput) (localapi.TaskMutationResult, error)
	DiffTask(context.Context, string, string) (application.TaskDiffView, error)
	SurveyRepairs(context.Context, string, localapi.SurveyRepairsInput) (application.RepairSurvey, error)
	ListDecisions(context.Context, string, localapi.ListDecisionsInput) (application.DecisionList, error)
	ShowDecision(context.Context, string, localapi.ShowDecisionInput) (application.TaskDecision, error)
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
	commandWorkerProfiles
	commandShowTask
	commandExplainTask
	commandGetLaunchPlan
	commandOperation
	commandPrepareTask
	commandReconcileTask
	commandHandbackTask
	commandCleanupTask
	commandDiscardTask
	commandPauseTask
	commandCancelTask
	commandResumeTask
	commandVerifyTask
	commandPromoteScout
	commandReplaceWorker
	commandSteerTask
	commandAttestScout
	commandListDecisions
	commandShowDecision
	commandDiffTask
	commandSurveyRepairs
)

type parsedCommand struct {
	kind            commandKind
	socketPath      string
	format          string
	reference       string
	decisionKey     string
	diffSelector    diffSelector
	inputPath       string
	operationID     string
	prepareInput    *localapi.PrepareTaskInput
	promoteInput    *localapi.PromoteScoutInput
	workerProfileID string
	instruction     string
	acknowledged    bool
	attestFinding   application.ScoutAttestationFinding
	attestKeys      []string
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
	if command.kind == commandPromoteScout {
		input, readErr := readPromoteInput(command.inputPath, config)
		if readErr != nil {
			return writeCLIOutput(stderr, "devcrew: invalid promotion contract\nHint: provide one strict bounded JSON input that does not name a scout\n", ExitUsage)
		}
		input.ScoutTaskHandle = command.reference
		command.promoteInput = &input
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
	case "workers":
		if len(args) < 2 || args[1] != "list" {
			return parsedCommand{}, errors.New("workers list is required")
		}
		format, err := parseFormat(args[2:], "table", "table", "json")
		if err != nil {
			return parsedCommand{}, err
		}
		command.kind, command.format = commandWorkerProfiles, format
	case "repair":
		return parseRepairCommand(command, args[1:])
	case "decisions":
		return parseDecisionsCommand(command, args[1:])
	case "decision":
		return parseDecisionCommand(command, args[1:])
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
	if len(args) > 0 && args[0] == "discard" {
		return parseDiscardTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "pause" {
		return parsePauseTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "cancel" {
		return parseCancelTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "resume" {
		return parseResumeTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "verify" {
		return parseVerifyTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "promote" {
		return parsePromoteScoutCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "replace" {
		return parseReplaceWorkerCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "steer" {
		return parseSteerTaskCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "attest" {
		return parseAttestScoutCommand(command, args[1:])
	}
	if len(args) > 0 && args[0] == "diff" {
		return parseTaskDiffCommand(command, args[1:])
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
	data, err := readBoundedContract(path, config)
	if err != nil {
		return localapi.PrepareTaskInput{}, err
	}
	return localapi.DecodePrepareTaskInput(data)
}

// readBoundedContract reads one strict bounded JSON contract from a file or
// stdin. Every command that accepts a contract shares it, so the size bound and
// the refusal to read an unavailable source cannot drift apart between them.
func readBoundedContract(path string, config Config) ([]byte, error) {
	var reader io.Reader
	var closer io.Closer
	if path == "-" {
		reader = config.Stdin
	} else {
		if config.OpenInput == nil {
			return nil, errors.New("input file access is unavailable")
		}
		opened, err := config.OpenInput(path)
		if err != nil {
			return nil, err
		}
		reader, closer = opened, opened
	}
	if reader == nil {
		return nil, errors.New("input is unavailable")
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}
	data, err := io.ReadAll(io.LimitReader(reader, localapi.MaxRequestBytes+1))
	if err != nil || len(data) > localapi.MaxRequestBytes {
		return nil, errors.New("read bounded contract")
	}
	return data, nil
}

// The promotion contract is read the same bounded way a preparation contract is.
// Sharing the reader keeps one size bound and one decode path rather than a
// second, subtly different one per command.
func readPromoteInput(path string, config Config) (localapi.PromoteScoutInput, error) {
	data, err := readBoundedContract(path, config)
	if err != nil {
		return localapi.PromoteScoutInput{}, err
	}
	return localapi.DecodePromoteScoutInput(data)
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
