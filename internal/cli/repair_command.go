package cli

import (
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

// parseRepairCommand parses the reconciliation survey.
//
// The survey reports and never acts. Reconciliation stays behind the explicit
// per-task command, so an operator always names the task whose state moves.
func parseRepairCommand(command parsedCommand, args []string) (parsedCommand, error) {
	if len(args) == 0 || args[0] != "reconcile" {
		return parsedCommand{}, errors.New("repair reconcile is required")
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
	command.kind, command.format = commandSurveyRepairs, format
	return command, nil
}

func renderRepairSurvey(destination io.Writer, survey application.RepairSurvey) error {
	if len(survey.Tasks) == 0 {
		if _, err := fmt.Fprintln(destination, "no task needs reconciliation"); err != nil {
			return fmt.Errorf("write repair survey: %w", err)
		}
		return nil
	}
	writer := tabwriter.NewWriter(destination, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "TASK\tSTATE\tPOSTURE\tNEXT SAFE ACTION"); err != nil {
		return fmt.Errorf("write repair header: %w", err)
	}
	for _, task := range survey.Tasks {
		if _, err := fmt.Fprintf(
			writer, "%s\t%s\t%s\t%s\n",
			task.TaskHandle, task.State, task.Posture, repairNextAction(task),
		); err != nil {
			return fmt.Errorf("write repair row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush repair survey: %w", err)
	}
	return nil
}

// repairNextAction names the move each posture calls for.
//
// The mapping lives in the console rather than in the report because it is
// guidance, not authority: the service decides what it will accept, and this
// text only tells an operator which command to type.
func repairNextAction(task application.TaskRepair) string {
	switch task.Posture {
	case application.RepairReconcilable:
		return "devcrew task reconcile " + task.TaskHandle + " --action validate-clean-candidate"
	case application.RepairWorktreeDirty:
		return "commit or discard the worktree changes, or hand the task back"
	case application.RepairNoCandidate:
		return "replace the worker; there is no candidate commit to keep"
	case application.RepairWorkspaceUnverified:
		return "restore the registered worktree before reconciling"
	case application.RepairAuthorityIncomplete:
		return "preserve the task; wait for terminal settlement"
	default:
		return "unknown"
	}
}
