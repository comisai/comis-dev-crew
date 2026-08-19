package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// parseDecisionsCommand parses the fleet inventory of open decisions.
//
// The task scope is sent to the service rather than filtered here, so naming one
// task never puts another task's private questions on the socket.
func parseDecisionsCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) == 0 || args[0] != "list" {
		return parsedCommand{}, errors.New("decisions list is required")
	}
	args = args[1:]
	if len(args) >= 2 && args[0] == "--task" {
		if err := domain.ValidateTaskHandle(args[1]); err != nil {
			return parsedCommand{}, err
		}
		command.reference = args[1]
		args = args[2:]
	}
	format, err := parseFormat(args, "table", "table", "json")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandListDecisions, format
	return command, nil
}

// parseDecisionCommand parses one keyed decision read.
//
// A decision is named by its task and its key because that is what identifies it
// durably; there is no separate decision identity to quote.
func parseDecisionCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) < 3 || args[0] != "show" {
		return parsedCommand{}, errors.New("decision show requires a task and a decision key")
	}
	if err := domain.ValidateTaskHandle(args[1]); err != nil {
		return parsedCommand{}, err
	}
	if err := domain.ValidateDecisionKey(args[2]); err != nil {
		return parsedCommand{}, err
	}
	format, err := parseFormat(args[3:], "text", "text", "json")
	if err != nil {
		return parsedCommand{}, err
	}
	command.kind, command.format = commandShowDecision, format
	command.reference, command.decisionKey = args[1], args[2]
	return command, nil
}

func renderDecisionList(destination io.Writer, list application.DecisionList) error {
	writer := tabwriter.NewWriter(destination, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "TASK\tDECISION\tSTATUS\tAIRINGS\tLAST AIRING\tNEXT AIRING\tQUESTION"); err != nil {
		return fmt.Errorf("write decision header: %w", err)
	}
	for _, decision := range list.Decisions {
		if _, err := fmt.Fprintf(
			writer, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			decision.TaskHandle, decision.ExternalKey, decision.Status, decision.Airings,
			renderDecisionTime(decision.LastAiringAt), renderDecisionTime(decision.NextAiringAt),
			decision.Question,
		); err != nil {
			return fmt.Errorf("write decision row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush decision listing: %w", err)
	}
	return nil
}

func renderDecisionDetail(destination io.Writer, decision application.TaskDecision) error {
	lines := [][2]string{
		{"task", decision.TaskHandle},
		{"decision", decision.ExternalKey},
		{"status", string(decision.Status)},
		{"reported", renderDecisionTime(&decision.ReportedAt)},
		{"first asked", renderDecisionTime(decision.AskedAt)},
		{"airings", fmt.Sprintf("%d", decision.Airings)},
		{"last airing", renderDecisionTime(decision.LastAiringAt)},
		{"next airing", renderDecisionTime(decision.NextAiringAt)},
		{"question", decision.Question},
	}
	if decision.Detail != "" {
		lines = append(lines, [2]string{"detail", decision.Detail})
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(destination, "%-12s %s\n", line[0], line[1]); err != nil {
			return fmt.Errorf("write decision detail: %w", err)
		}
	}
	return nil
}

// renderDecisionTime renders an absent time as unknown rather than as an epoch.
//
// A zero instant would read as long overdue, which is the opposite of what an
// absent airing means: nobody has been asked yet.
func renderDecisionTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339)
}
