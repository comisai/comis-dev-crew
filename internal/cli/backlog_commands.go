package cli

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func parseBacklogCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) == 0 {
		return parsedCommand{}, errors.New("backlog subcommand is required")
	}
	switch args[0] {
	case "add":
		command.kind = commandAddBacklog
		return parseBacklogMutationOptions(command, args[1:])
	case "promote":
		if len(args) < 2 || domain.ValidateTaskHandle(args[1]) != nil {
			return parsedCommand{}, errors.New("backlog promotion handle is required")
		}
		command.kind, command.reference = commandPromoteBacklog, args[1]
		return parseBacklogMutationOptions(command, args[2:])
	default:
		return parsedCommand{}, errors.New("unknown backlog command")
	}
}

func parseBacklogMutationOptions(command parsedCommand, args []string) (parsedCommand, error) {
	command.format = "json"
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid backlog mutation arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--input":
			if value == "" {
				return parsedCommand{}, errors.New("backlog input is required")
			}
			command.inputPath = value
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid backlog operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("backlog mutation format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown backlog mutation option")
		}
		args = args[2:]
	}
	if command.inputPath == "" {
		return parsedCommand{}, errors.New("backlog input is required")
	}
	return command, nil
}
