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
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// defaultWatchPasses bounds a watch invocation that states no count, so the
// command always terminates rather than running until it is killed.
const defaultWatchPasses = 1

// parseEventsCommand parses one pass over the content-free service stream.
//
// Following is a caller-side loop over the cursor rather than a held connection:
// the log is durable and resumable, so a follower that drops reconnects at the
// sequence it last saw and neither replays nor skips.
func parseEventsCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) == 0 || args[0] != "tail" {
		return parsedCommand{}, errors.New("events tail is required")
	}
	args = args[1:]
	if len(args) >= 2 && args[0] == "--after" {
		cursor, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil || cursor < 0 {
			return parsedCommand{}, errors.New("event cursor must be a non-negative number")
		}
		command.eventCursor = cursor
		args = args[2:]
	}
	if len(args) >= 2 && args[0] == "--task" {
		if err := domain.ValidateTaskHandle(args[1]); err != nil {
			return parsedCommand{}, err
		}
		command.reference = args[1]
		args = args[2:]
	}
	format, err := parseFormat(args, "text", "text", "jsonl")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandReadEvents, format
	return command, nil
}

func renderEventPage(destination io.Writer, command parsedCommand, page application.EventPage) error {
	if command.format == "jsonl" {
		// One event per line so a follower consumes it incrementally instead of
		// waiting for a document to close.
		encoder := json.NewEncoder(destination)
		for _, event := range page.Events {
			if err := encoder.Encode(event); err != nil {
				return fmt.Errorf("write event line: %w", err)
			}
		}
		return nil
	}
	writer := tabwriter.NewWriter(destination, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "SEQ\tOBSERVED\tKIND\tTASK\tSTATE\tREASON"); err != nil {
		return fmt.Errorf("write event header: %w", err)
	}
	for _, event := range page.Events {
		if _, err := fmt.Fprintf(
			writer, "%d\t%s\t%s\t%s\t%s\t%s\n",
			event.Sequence, event.OccurredAt.UTC().Format(time.RFC3339),
			event.Kind, renderEventField(event.TaskHandle),
			renderEventField(string(event.State)), renderEventField(event.Reason),
		); err != nil {
			return fmt.Errorf("write event row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush event stream: %w", err)
	}
	if _, err := fmt.Fprintf(destination, "resume with --after %d\n", page.NextCursor); err != nil {
		return fmt.Errorf("write event cursor: %w", err)
	}
	return nil
}

func renderEventField(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// parseStatusCommand parses the fleet dashboard and its bounded watch mode.
//
// Watch re-reads the authoritative snapshot on every pass instead of mutating a
// display from events. A dropped or reordered event can then never leave the
// view claiming a state the service is not in — the snapshot is the truth and
// the stream only says when to look again.
func parseStatusCommand(command parsedCommand, args []string) (parsedCommand, error) {
	command.watchInterval = 2 * time.Second
	if len(args) > 0 && args[0] == "--watch" {
		command.watchPasses = defaultWatchPasses
		args = args[1:]
		for len(args) >= 2 && (args[0] == "--passes" || args[0] == "--interval") {
			if args[0] == "--passes" {
				passes, err := strconv.Atoi(args[1])
				if err != nil || passes < 1 {
					return parsedCommand{}, errors.New("watch passes must be a positive number")
				}
				command.watchPasses = passes
			} else {
				interval, err := time.ParseDuration(args[1])
				if err != nil || interval < 0 {
					return parsedCommand{}, errors.New("watch interval must be a non-negative duration")
				}
				command.watchInterval = interval
			}
			args = args[2:]
		}
	}
	format, err := parseFormat(args, "table", "table", "json")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandFleet, format
	return command, nil
}
