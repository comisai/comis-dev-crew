package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// diffSelector chooses between the two bounded renderings of one change set.
type diffSelector string

const (
	diffStat     diffSelector = "stat"
	diffNameOnly diffSelector = "name-only"
)

// parseTaskDiffCommand parses one bounded change summary request.
//
// Only summaries are offered. A patch body is unbounded worker-authored content,
// so there is no flag that would produce one: the surface cannot be asked for
// something it could not bound.
func parseTaskDiffCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 1 {
		return parsedCommand{}, errors.New("task diff requires a task reference")
	}
	if err := domain.ValidateTaskHandle(args[0]); err != nil {
		return parsedCommand{}, err
	}
	command.reference = args[0]
	command.diffSelector = diffStat
	args = args[1:]
	if len(args) > 0 && (args[0] == "--stat" || args[0] == "--name-only") {
		if args[0] == "--name-only" {
			command.diffSelector = diffNameOnly
		}
		args = args[1:]
	}
	format, err := parseFormat(args, "text", "text", "json")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandDiffTask, format
	return command, nil
}

func renderTaskDiff(destination io.Writer, command parsedCommand, view application.TaskDiffView) error {
	sections := []struct {
		name    string
		changes []application.TaskFileChange
		totals  application.TaskDiffTotals
	}{
		{"committed", view.Committed, view.CommittedTotals},
		{"uncommitted", view.Uncommitted, view.UncommittedTotals},
	}
	for _, section := range sections {
		if err := renderDiffSection(destination, command.diffSelector, section.name, section.changes, section.totals); err != nil {
			return err
		}
	}
	// Stated rather than implied: an operator deciding from a partial file list
	// has to know the change set was larger than this read bounds.
	if view.FileListTruncated {
		if _, err := fmt.Fprintln(destination, "note  the change set outgrew this read; the file list is truncated"); err != nil {
			return fmt.Errorf("write diff truncation note: %w", err)
		}
	}
	return nil
}

func renderDiffSection(
	destination io.Writer,
	selector diffSelector,
	name string,
	changes []application.TaskFileChange,
	totals application.TaskDiffTotals,
) error {
	if _, err := fmt.Fprintf(destination, "%s (%d files)\n", name, totals.Files); err != nil {
		return fmt.Errorf("write diff section: %w", err)
	}
	if selector == diffNameOnly {
		for _, change := range changes {
			if _, err := fmt.Fprintf(destination, "  %s\n", renderDiffPath(change)); err != nil {
				return fmt.Errorf("write diff path: %w", err)
			}
		}
		return nil
	}
	writer := tabwriter.NewWriter(destination, 0, 0, 2, ' ', 0)
	for _, change := range changes {
		extent := "binary"
		if !change.Binary {
			extent = fmt.Sprintf("+%d/-%d", change.Added, change.Deleted)
		}
		if _, err := fmt.Fprintf(writer, "  %s\t%s\n", extent, renderDiffPath(change)); err != nil {
			return fmt.Errorf("write diff row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush diff section: %w", err)
	}
	if totals.Files != 0 {
		if _, err := fmt.Fprintf(
			destination, "  total +%d/-%d across %d files (%d binary)\n",
			totals.Added, totals.Deleted, totals.Files, totals.BinaryFiles,
		); err != nil {
			return fmt.Errorf("write diff totals: %w", err)
		}
	}
	return nil
}

// renderDiffPath keeps a rename's previous path attached to its current one, so
// the work is never attributed to a file that no longer exists.
func renderDiffPath(change application.TaskFileChange) string {
	if change.PreviousPath != "" {
		return change.PreviousPath + " -> " + change.Path
	}
	return change.Path
}
