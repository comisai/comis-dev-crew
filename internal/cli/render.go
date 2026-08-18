package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/comisai/comis-dev-crew/internal/application"
)

func renderResult(destination io.Writer, command parsedCommand, result any) error {
	if command.format == "json" {
		return renderJSON(destination, result)
	}
	switch command.kind {
	case commandServiceStatus:
		return renderServiceStatus(destination, result.(application.DiagnosticReport))
	case commandDoctor:
		return renderDoctor(destination, result.(application.DiagnosticReport))
	case commandFleet:
		return renderFleet(destination, result.(application.FleetSnapshot))
	case commandListTasks:
		return renderTaskList(destination, result.(application.TaskList))
	case commandWorkerProfiles:
		return renderWorkerProfiles(destination, result.(application.WorkerProfileList))
	case commandShowTask:
		return renderTaskYAML(destination, result.(application.TaskDetail))
	case commandExplainTask:
		return renderExplanation(destination, result.(application.TaskExplanation))
	case commandOperation:
		return renderOperation(destination, result.(application.OperationView))
	default:
		return errors.New("render unknown command")
	}
}

func renderJSON(destination io.Writer, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode JSON output: %w", err)
	}
	return nil
}

func renderServiceStatus(destination io.Writer, report application.DiagnosticReport) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "SERVICE\tHEALTH\tCOMPLETENESS\tSTATE VERSION"); err != nil {
			return err
		}
		_, err := fmt.Fprintf(table, "devcrew-service\t%s\t%s\t%d\n", report.ServiceHealth, report.Completeness, report.StateVersion)
		return err
	})
}

func renderDoctor(destination io.Writer, report application.DiagnosticReport) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "CHECK\tSTATUS\tMESSAGE\tHINT"); err != nil {
			return err
		}
		for _, check := range report.Checks {
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", check.Name, check.Status, check.Message, check.Hint); err != nil {
				return err
			}
		}
		return nil
	})
}

func renderFleet(destination io.Writer, snapshot application.FleetSnapshot) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "TASK\tINIT/COMPONENT\tSTATE\tCUSTODY\tWORKER\tHEAD\tACTIVITY\tPROCESSES\tVALIDATION\tBLOCKED BY\tATTENTION\tNEXT"); err != nil {
			return err
		}
		for _, task := range snapshot.Tasks {
			if _, err := fmt.Fprintf(table, "%s\t-\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				task.TaskHandle, task.State, task.Custody, task.WorkerProfileID, task.Head,
				task.Activity, task.Processes, task.Validation, task.BlockedBy, task.Attention,
				joinActions(task.NextSafeActions)); err != nil {
				return err
			}
		}
		return nil
	})
}

func renderTaskList(destination io.Writer, list application.TaskList) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "TASK\tSTATE\tWORKER\tREPOSITORY\tNEXT"); err != nil {
			return err
		}
		for _, task := range list.Tasks {
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", task.TaskHandle, task.State, task.WorkerProfileID, task.RepositoryID, joinActions(task.NextSafeActions)); err != nil {
				return err
			}
		}
		return nil
	})
}

// An unavailable profile renders its reason in the same row. An operator asking
// "why can nothing start" gets the answer from the listing itself, without a
// second command to find out what "unavailable" meant.
func renderWorkerProfiles(destination io.Writer, list application.WorkerProfileList) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "PROFILE\tHARNESS\tSHAPES\tAVAILABILITY\tUNATTENDED\tLIMIT"); err != nil {
			return err
		}
		for _, profile := range list.Profiles {
			availability := profile.Availability
			if profile.AvailabilityReason != "" {
				availability += " (" + profile.AvailabilityReason + ")"
			}
			shapes := make([]string, 0, len(profile.AllowedShapes))
			for _, shape := range profile.AllowedShapes {
				shapes = append(shapes, string(shape))
			}
			if _, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%t\t%d\n",
				profile.ProfileID, profile.Harness, strings.Join(shapes, ","),
				availability, profile.Unattended, profile.ConcurrencyLimit); err != nil {
				return err
			}
		}
		return nil
	})
}

func renderTaskYAML(destination io.Writer, detail application.TaskDetail) error {
	lines := []string{
		"schemaVersion: " + strconv.Itoa(detail.SchemaVersion),
		"capturedAtMs: " + strconv.FormatInt(detail.CapturedAtMs, 10),
		"completeness: " + quote(string(detail.Completeness)),
		"taskHandle: " + quote(detail.Summary.TaskHandle),
		"state: " + quote(string(detail.Summary.State)),
		"stateReason: " + quote(detail.Summary.StateReason),
		"stateSource: " + quote(string(detail.Summary.StateSource)),
		"stateConfidence: " + quote(string(detail.Summary.StateConfidence)),
		"freshness: " + quote(string(detail.Summary.Freshness)),
		"shape: " + quote(string(detail.Shape)),
		"repositoryId: " + quote(detail.Summary.RepositoryID),
		"baseRevision: " + quote(detail.BaseRevision),
		"briefRevision: " + strconv.FormatInt(detail.BriefRevision, 10),
		"validationProfile: " + quote(detail.ValidationProfile),
		"deliveryMode: " + quote(string(detail.DeliveryMode)),
		"workerProfileId: " + quote(detail.Summary.WorkerProfileID),
		"reportCursor: " + strconv.FormatInt(detail.ReportCursor, 10),
		"evidence:",
		"  candidate:",
		"    status: " + quote(string(detail.Evidence.Candidate.Status)),
		"    headRevision: " + quote(detail.Evidence.Candidate.HeadRevision),
		"    evidenceDigest: " + quote(detail.Evidence.Candidate.EvidenceDigest),
		"    reconciliationOperationId: " + quote(detail.Evidence.Candidate.ReconciliationOperationID),
		"  activity:",
		"    status: " + quote(string(detail.Evidence.Activity.Status)),
		"    reportId: " + quote(detail.Evidence.Activity.ReportID),
		"    reportKind: " + quote(string(detail.Evidence.Activity.ReportKind)),
		"    acceptedAtMs: " + strconv.FormatInt(detail.Evidence.Activity.AcceptedAtMs, 10),
		"  decision:",
		"    status: " + quote(string(detail.Evidence.Decision.Status)),
		"    decisionReportId: " + quote(detail.Evidence.Decision.DecisionReportID),
		"    resolutionReportId: " + quote(detail.Evidence.Decision.ResolutionReportID),
		"  validation:",
		"    status: " + quote(string(detail.Evidence.Validation.Status)),
		"    evidenceDigest: " + quote(detail.Evidence.Validation.EvidenceDigest),
		"    processOperationId: " + quote(detail.Evidence.Validation.ProcessOperationID),
		"  delivery:",
		"    status: " + quote(string(detail.Evidence.Delivery.Status)),
		"    evidenceOperationId: " + quote(detail.Evidence.Delivery.EvidenceOperationID),
		"    evidenceRef: " + quote(detail.Evidence.Delivery.EvidenceRef),
		"    pullRequestId: " + quote(detail.Evidence.Delivery.PullRequestID),
		"  cleanup:",
		"    status: " + quote(string(detail.Evidence.Cleanup.Status)),
		"    operationId: " + quote(detail.Evidence.Cleanup.OperationID),
		"    openHoldCount: " + strconv.Itoa(detail.Evidence.Cleanup.OpenHoldCount),
		"  authority:",
		"    managedRunId: " + quote(detail.Evidence.Authority.ManagedRunID),
		"    workspaceLeaseId: " + quote(detail.Evidence.Authority.WorkspaceLeaseID),
		"    executionAttachmentId: " + quote(detail.Evidence.Authority.ExecutionAttachmentID),
		"    preparationOperationId: " + quote(detail.Evidence.Authority.PreparationOperationID),
		"stateVersion: " + strconv.FormatInt(detail.StateVersion, 10),
		"createdAtMs: " + strconv.FormatInt(detail.CreatedAtMs, 10),
		"updatedAtMs: " + strconv.FormatInt(detail.UpdatedAtMs, 10),
	}
	if _, err := io.WriteString(destination, strings.Join(lines, "\n")+"\n"); err != nil {
		return fmt.Errorf("write YAML output: %w", err)
	}
	return nil
}

func renderExplanation(destination io.Writer, explanation application.TaskExplanation) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "TASK\tSTATE\tREASON\tLIKELY ROOT CAUSE\tNEXT"); err != nil {
			return err
		}
		_, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n", explanation.Summary.TaskHandle, explanation.Summary.State, explanation.ReasonCode, explanation.LikelyRootCause, joinActions(explanation.NextSafeActions))
		return err
	})
}

func renderOperation(destination io.Writer, operation application.OperationView) error {
	return writeTable(destination, func(table *tabwriter.Writer) error {
		if _, err := fmt.Fprintln(table, "OPERATION\tCOMMAND\tSTATUS\tERROR\tSTATE VERSION"); err != nil {
			return err
		}
		_, err := fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%d\n", operation.OperationID, operation.Command, operation.Status, operation.ErrorCode, operation.StateVersion)
		return err
	})
}

func writeTable(destination io.Writer, render func(*tabwriter.Writer) error) error {
	table := tabwriter.NewWriter(destination, 0, 4, 2, ' ', 0)
	if err := render(table); err != nil {
		return fmt.Errorf("render table: %w", err)
	}
	if err := table.Flush(); err != nil {
		return fmt.Errorf("flush table: %w", err)
	}
	return nil
}

func joinActions(actions []application.NextAction) string {
	if len(actions) == 0 {
		return "unknown"
	}
	values := make([]string, 0, len(actions))
	for _, action := range actions {
		values = append(values, string(action))
	}
	return strings.Join(values, ",")
}

func quote(value string) string {
	return strconv.Quote(value)
}
