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
