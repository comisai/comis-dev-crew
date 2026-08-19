package cli

import (
	"context"
	"io"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// runPasses executes the command once, or once per watch pass.
//
// Each pass re-reads the authoritative snapshot under its own operation
// identity: a watch is a sequence of independent reads, not one long-lived
// subscription whose display drifts from the service.
func runPasses(
	ctx context.Context,
	client ReadClient,
	operationID string,
	command parsedCommand,
	stdout, stderr io.Writer,
	config Config,
) int {
	passes := command.watchPasses
	if passes < 1 {
		passes = 1
	}
	pause := config.Sleep
	if pause == nil {
		pause = time.Sleep
	}
	for pass := 0; pass < passes; pass++ {
		if pass != 0 {
			if err := ctx.Err(); err != nil {
				return ExitSuccess
			}
			pause(command.watchInterval)
			next, err := config.NewOperationID()
			if err != nil || domain.ValidateOperationID(next) != nil {
				return writeCLIOutput(stderr, "devcrew: request identity is unavailable\nHint: retry after checking local entropy\n", ExitUnavailable)
			}
			operationID = next
		}
		result, err := execute(ctx, client, operationID, command)
		if err != nil {
			return renderFailure(stderr, err)
		}
		if err := renderResult(stdout, command, result); err != nil {
			return writeCLIOutput(stderr, "devcrew: output is unavailable\nHint: inspect the output destination\n", ExitUnavailable)
		}
	}
	return ExitSuccess
}
