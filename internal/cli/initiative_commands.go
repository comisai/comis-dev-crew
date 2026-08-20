package cli

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func parseInitiativeCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) == 0 {
		return parsedCommand{}, errors.New("initiative subcommand is required")
	}
	if args[0] == "list" {
		return parseInitiativeListCommand(command, args[1:])
	}
	if len(args) < 2 {
		return parsedCommand{}, errors.New("initiative reference is required")
	}
	command.reference = args[1]
	if err := domain.ValidateTaskHandle(command.reference); err != nil {
		return parsedCommand{}, err
	}
	switch args[0] {
	case "show":
		command.kind = commandShowInitiative
	case "explain":
		command.kind = commandExplainInitiative
	case "graph":
		command.kind = commandGraphInitiative
	default:
		return parsedCommand{}, errors.New("unknown initiative command")
	}
	format, err := parseFormat(args[2:], "text", "text", "json")
	if err != nil {
		return parsedCommand{}, err
	}
	command.format = format
	return command, nil
}

func parseInitiativeListCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) >= 2 && args[0] == "--state" {
		state := domain.InitiativeState(args[1])
		if err := domain.ValidateInitiativeState(state); err != nil {
			return parsedCommand{}, err
		}
		command.initiativeState = args[1]
		args = args[2:]
	}
	format, err := parseFormat(args, "table", "table", "json")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandListInitiatives, format
	return command, nil
}
