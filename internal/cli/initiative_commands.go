package cli

import (
	"errors"
	"strconv"
	"time"

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
	case "watch":
		return parseInitiativeWatchCommand(command, args[2:])
	case "pause":
		return parseInitiativeControlCommand(command, commandPauseInitiative, args[2:])
	case "resume":
		return parseInitiativeControlCommand(command, commandResumeInitiative, args[2:])
	case "cancel":
		return parseInitiativeControlCommand(command, commandCancelInitiative, args[2:])
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

func parseInitiativeWatchCommand(command parsedCommand, args []string) (parsedCommand, error) {
	command.kind, command.format = commandWatchInitiative, "text"
	command.watchPasses, command.watchInterval = defaultWatchPasses, 2*time.Second
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid initiative watch arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--passes":
			passes, err := strconv.Atoi(value)
			if err != nil || passes < 1 {
				return parsedCommand{}, errors.New("watch passes must be a positive number")
			}
			command.watchPasses = passes
		case "--interval":
			interval, err := time.ParseDuration(value)
			if err != nil || interval < 0 {
				return parsedCommand{}, errors.New("watch interval must be a non-negative duration")
			}
			command.watchInterval = interval
		default:
			return parsedCommand{}, errors.New("unknown initiative watch option")
		}
		args = args[2:]
	}
	return command, nil
}

func parseInitiativeControlCommand(
	command parsedCommand,
	kind commandKind,
	args []string,
) (parsedCommand, error) {
	command.kind, command.format = kind, "json"
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid initiative control arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid initiative control operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("initiative control format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown initiative control option")
		}
		args = args[2:]
	}
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
