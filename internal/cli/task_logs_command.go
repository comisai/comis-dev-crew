package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// parseTaskLogsCommand parses one bounded read of a task's private history.
//
// Following is bounded by passes so the command always terminates, and each pass
// resumes from the cursor the previous page returned rather than re-reading from
// the beginning.
func parseTaskLogsCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 {
		return parsedCommand{}, errors.New("task logs requires a task reference")
	}
	if err := domain.ValidateTaskHandle(args[0]); err != nil {
		return parsedCommand{}, err
	}
	command.reference = args[0]
	command.logSource = application.LogSourceWorker
	command.watchInterval = 2 * time.Second
	args = args[1:]
	for len(args) > 0 {
		switch {
		case args[0] == "--source" && len(args) >= 2:
			source := application.TaskLogSource(args[1])
			if !source.Valid() {
				return parsedCommand{}, errors.New("log source is not a known value")
			}
			command.logSource, args = source, args[2:]
		case args[0] == "--follow":
			command.watchPasses, args = defaultWatchPasses, args[1:]
		case args[0] == "--passes" && len(args) >= 2 && command.watchPasses > 0:
			passes, err := strconv.Atoi(args[1])
			if err != nil || passes < 1 {
				return parsedCommand{}, errors.New("follow passes must be a positive number")
			}
			command.watchPasses, args = passes, args[2:]
		default:
			format, err := parseFormat(args, "text", "text", "json")
			if err != nil {
				return parsedCommand{}, err
			}
			command.kind, command.format = commandReadTaskLogs, format
			return command, nil
		}
	}
	command.kind, command.format = commandReadTaskLogs, "text"
	return command, nil
}

func renderTaskLogPage(destination io.Writer, page application.TaskLogPage) error {
	writer := tabwriter.NewWriter(destination, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SEQ\tOBSERVED\tSOURCE\tLABEL\tDETAIL"); err != nil {
		return fmt.Errorf("write log header: %w", err)
	}
	for _, entry := range page.Entries {
		detail := entry.Detail
		if entry.Outcome != "" {
			detail = detail + " (" + entry.Outcome + ")"
		}
		if _, err := fmt.Fprintf(
			writer, "%d\t%s\t%s\t%s\t%s\n",
			entry.Sequence, entry.OccurredAt.UTC().Format(time.RFC3339),
			entry.Source, entry.Label, detail,
		); err != nil {
			return fmt.Errorf("write log row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush task logs: %w", err)
	}
	return nil
}
