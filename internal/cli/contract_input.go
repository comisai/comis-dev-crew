package cli

import (
	"errors"
	"io"

	"github.com/comisai/comis-dev-crew/internal/localapi"
)

// applyContractInput resolves every command that carries a bounded JSON contract
// before a client exists. Reading and decoding happen in one place so a new
// contract-bearing command cannot quietly skip the size bound or the strict
// decode that keeps operator input from reaching a task unchecked.
//
// It reports the operator message and exit code when a contract is unusable, and
// whether it handled the command at all.
func applyContractInput(command *parsedCommand, config Config) (string, int, bool) {
	if command.kind == commandPrepareTask {
		input, readErr := readPrepareInput(command.inputPath, config)
		if readErr != nil {
			return "devcrew: invalid task contract\nHint: provide one strict bounded JSON input\n", ExitUsage, true
		}
		command.prepareInput = &input
	}
	if command.kind == commandPromoteScout {
		input, readErr := readPromoteInput(command.inputPath, config)
		if readErr != nil {
			return "devcrew: invalid promotion contract\nHint: provide one strict bounded JSON input that does not name a scout\n", ExitUsage, true
		}
		input.ScoutTaskHandle = command.reference
		command.promoteInput = &input
	}
	if command.kind == commandRespondDecision {
		data, readErr := readBoundedContract(command.inputPath, config)
		if readErr != nil {
			return "devcrew: invalid answer contract\nHint: provide one strict bounded JSON input\n", ExitUsage, true
		}
		input, decodeErr := localapi.DecodeRespondDecisionInput(data)
		if decodeErr != nil {
			return "devcrew: invalid answer contract\nHint: provide one strict bounded JSON input\n", ExitUsage, true
		}
		command.decisionAnswer = input.Response
	}
	if command.kind == commandSteerTask {
		data, readErr := readBoundedContract(command.inputPath, config)
		if readErr != nil {
			return "devcrew: invalid steer contract\nHint: provide one strict bounded JSON input\n", ExitUsage, true
		}
		input, decodeErr := localapi.DecodeSteerTaskInput(data)
		if decodeErr != nil {
			return "devcrew: invalid steer contract\nHint: provide one strict bounded JSON input\n", ExitUsage, true
		}
		command.instruction = input.Instruction
	}
	return "", 0, false
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
