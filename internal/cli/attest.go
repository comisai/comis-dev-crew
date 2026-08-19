package cli

import (
	"errors"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// parseAttestScoutCommand reads the scout and the liaison's stated inventory.
//
// The finding is a required argument rather than something inferred from an
// absent key list. An operator who typed nothing has told the service nothing,
// and reading that as "no decisions were open" is exactly the silence this
// record exists to refuse.
func parseAttestScoutCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 || domain.ValidateTaskHandle(args[0]) != nil {
		return parsedCommand{}, errors.New("attest task reference is required")
	}
	command.kind = commandAttestScout
	command.reference = args[0]
	command.format = "json"
	args = args[1:]
	seen := make(map[string]bool)
	for len(args) > 0 {
		if len(args) < 2 || seen[args[0]] {
			return parsedCommand{}, errors.New("invalid attest arguments")
		}
		name, value := args[0], args[1]
		seen[name] = true
		switch name {
		case "--finding":
			finding := application.ScoutAttestationFinding(value)
			if finding != application.ScoutAttestationOpenDecisions &&
				finding != application.ScoutAttestationNoOpenDecisions {
				return parsedCommand{}, errors.New("invalid attest finding")
			}
			command.attestFinding = finding
		case "--open-decision":
			if domain.ValidateDecisionKey(value) != nil {
				return parsedCommand{}, errors.New("invalid attest decision key")
			}
			// Repeated for each key, so a shell cannot smuggle a list through
			// one argument and each key is validated on its own.
			seen[name] = false
			command.attestKeys = append(command.attestKeys, value)
		case "--operation":
			if domain.ValidateOperationID(value) != nil {
				return parsedCommand{}, errors.New("invalid attest operation")
			}
			command.operationID = value
		case "--format":
			if value != "json" {
				return parsedCommand{}, errors.New("attest format must be JSON")
			}
		default:
			return parsedCommand{}, errors.New("unknown attest option")
		}
		args = args[2:]
	}
	if command.attestFinding == "" {
		return parsedCommand{}, errors.New("attest finding is required")
	}
	return command, nil
}
