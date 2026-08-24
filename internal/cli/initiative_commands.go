package cli

import (
	"errors"
	"strconv"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
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
	case "integrate":
		return parseInitiativeIntegrationCommand(command, args[2:])
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

func parseInitiativeIntegrationCommand(command parsedCommand, args []string) (parsedCommand, error) {
	command.kind, command.format = commandApplyIntegration, "json"
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid initiative integration arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--input":
			if value == "" {
				return parsedCommand{}, errors.New("initiative integration input is required")
			}
			command.inputPath = value
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid initiative integration operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("initiative integration format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown initiative integration option")
		}
		args = args[2:]
	}
	if command.inputPath == "" {
		return parsedCommand{}, errors.New("initiative integration input is required")
	}
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
	command.kind, command.format = commandListInitiatives, "table"
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid initiative list arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--state":
			state := domain.InitiativeState(value)
			if err := domain.ValidateInitiativeState(state); err != nil {
				return parsedCommand{}, err
			}
			command.initiativeState = value
		case "--after":
			if domain.ValidateTaskHandle(value) != nil {
				return parsedCommand{}, errors.New("initiative list cursor is invalid")
			}
			command.initiativeCursor = value
		case "--limit":
			limit, err := strconv.Atoi(value)
			if err != nil || limit < 1 || limit > application.MaximumInitiativePage {
				return parsedCommand{}, errors.New("initiative list limit is invalid")
			}
			command.initiativeLimit = limit
		case "--format":
			if value != "table" && value != "json" {
				return parsedCommand{}, errors.New("initiative list format is invalid")
			}
			command.format = value
		default:
			return parsedCommand{}, errors.New("unknown initiative list option")
		}
		args = args[2:]
	}
	return command, nil
}
