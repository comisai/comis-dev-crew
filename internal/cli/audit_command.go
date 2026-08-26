package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// parseAuditCommand parses one pass over the durable audit trail.
//
// It takes no task scope. The trail is short, and a reader filtering it to one
// task would miss exactly the pattern worth seeing — the same rejection arriving
// against several tasks in turn.
func parseAuditCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) == 0 || args[0] != "tail" {
		return parsedCommand{}, errors.New("audit tail is required")
	}
	args = args[1:]
	if len(args) >= 2 && args[0] == "--after" {
		cursor, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || cursor < 0 {
			return parsedCommand{}, errors.New("audit cursor must be a non-negative number")
		}
		command.eventCursor = cursor
		args = args[2:]
	}
	format, err := parseFormat(args, "text", "text", "jsonl")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandReadAudit, format
	return command, nil
}

func renderAuditPage(destination io.Writer, command parsedCommand, page application.AuditPage) error {
	if command.format == "jsonl" {
		encoder := json.NewEncoder(destination)
		for _, event := range page.Events {
			if err := encoder.Encode(event); err != nil {
				return fmt.Errorf("write audit line: %w", err)
			}
		}
		return nil
	}
	writer := tabwriter.NewWriter(destination, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SEQ\tOBSERVED\tKIND\tTASK\tREASON"); err != nil {
		return fmt.Errorf("write audit header: %w", err)
	}
	for _, event := range page.Events {
		if _, err := fmt.Fprintf(
			writer, "%d\t%s\t%s\t%s\t%s\n",
			event.Sequence, event.OccurredAt.UTC().Format(time.RFC3339),
			event.Kind, renderEventField(event.TaskHandle), renderEventField(string(event.Reason)),
		); err != nil {
			return fmt.Errorf("write audit row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush audit trail: %w", err)
	}
	if _, err := fmt.Fprintf(destination, "resume with --after %d\n", page.NextCursor); err != nil {
		return fmt.Errorf("write audit cursor: %w", err)
	}
	return nil
}
