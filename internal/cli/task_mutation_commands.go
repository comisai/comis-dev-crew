package cli

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

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

// parsePauseTaskCommand reads one task reference and nothing else. Pause takes
// no instruction text and no interrupt flag: adding either here would make one
// command mean two different things to the worker, and the operator could not
// tell from the transcript which one they had asked for.
func parsePauseTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("pause task reference is required")
	}
	command.kind = commandPauseTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid pause arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid pause operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("pause format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown pause option")
		}
		args = args[2:]
	}
	return command, nil
}

// parseCancelTaskCommand reads one task reference. Cancel names no disposition:
// it preserves artifacts, and removing them is cleanup's separate decision.
func parseCancelTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("cancel task reference is required")
	}
	command.kind = commandCancelTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid cancel arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid cancel operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("cancel format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown cancel option")
		}
		args = args[2:]
	}
	return command, nil
}

// parseResumeTaskCommand reads one task reference. Resume selects no worker:
// continuing what was already running and choosing a different worker are
// different decisions with different safety requirements.
func parseResumeTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("resume task reference is required")
	}
	command.kind = commandResumeTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid resume arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid resume operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("resume format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown resume option")
		}
		args = args[2:]
	}
	return command, nil
}

// parseVerifyTaskCommand reads one task reference. Verify selects no profile or
// checks of its own: validation runs the reviewed profile the task was prepared
// with, and a caller able to choose otherwise could validate against an easier
// bar than the one the task was accepted under.
func parseVerifyTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("verify task reference is required")
	}
	command.kind = commandVerifyTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid verify arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid verify operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("verify format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown verify option")
		}
		args = args[2:]
	}
	return command, nil
}

// parsePromoteScoutCommand reads the scout handle from the command line and the
// ship contract from a bounded JSON input.
//
// The handle stays on the command line deliberately. An operator promoting a
// scout names it where they can see it, and a contract that could also name one
// would let the file disagree with the command — with no way to tell afterwards
// which the service acted on.
func parsePromoteScoutCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("promote scout reference is required")
	}
	command.kind = commandPromoteScout
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid promote arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--input":
			if value == "" {
				return parsedCommand{}, errors.New("invalid promote input")
			}
			command.inputPath = value
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid promote operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("promote format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown promote option")
		}
		args = args[2:]
	}
	if command.inputPath == "" {
		return parsedCommand{}, errors.New("promote input is required")
	}
	return command, nil
}

// parseReplaceWorkerCommand reads the task and the profile that takes over.
// Both are required: a replacement with no named profile would have to pick one,
// and a command that chose a worker on the operator's behalf is exactly the
// decision this command exists to make explicit.
func parseReplaceWorkerCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("replace task reference is required")
	}
	command.kind = commandReplaceWorker
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid replace arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--worker":
			if domain.ValidateAuthorityReference("workerProfileId", value) != nil {
				return parsedCommand{}, errors.New("invalid replacement worker profile")
			}
			command.workerProfileID = value
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid replace operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("replace format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown replace option")
		}
		args = args[2:]
	}
	if command.workerProfileID == "" {
		return parsedCommand{}, errors.New("replacement worker profile is required")
	}
	return command, nil
}

// parseSteerTaskCommand reads the task and one bounded instruction.
//
// The instruction is typed once, on the command line, and the service stores it
// once. There is no retry flag: an uncertain send is reconciled against its
// operation rather than re-typed, because re-sending would queue the same words
// twice and the worker would act on them twice.
func parseSteerTaskCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("steer task reference is required")
	}
	command.kind = commandSteerTask
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid steer arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--instruction":
			if domain.ValidateSteeringInstruction(value) != nil {
				return parsedCommand{}, errors.New("invalid steering instruction")
			}
			command.instruction = value
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid steer operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("steer format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown steer option")
		}
		args = args[2:]
	}
	if command.instruction == "" {
		return parsedCommand{}, errors.New("steering instruction is required")
	}
	return command, nil
}
