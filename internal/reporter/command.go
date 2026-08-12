package reporter

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// RuntimeCapability is the complete authority available to devcrew-report.
// Task and run selectors are intentionally absent: the protected attachment
// already binds both operations to one exact task.
type RuntimeCapability interface {
	Brief(context.Context) (domain.WorkerBrief, error)
	Report(context.Context, domain.WorkerReport) (domain.ReportReceipt, error)
	Acknowledge(context.Context, string) error
}

// CommandConfig supplies composition-root dependencies without exposing them
// as worker-controlled command-line arguments.
type CommandConfig struct {
	Capability       RuntimeCapability
	Clock            func() time.Time
	NewLocalReportID func() (string, error)
	WorkingDirectory func() (string, error)
	Version          string
}

// RunCommand executes the closed devcrew-report command vocabulary.
func RunCommand(ctx context.Context, args []string, stdout, stderr io.Writer, config CommandConfig) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h":
			writeCommandUsage(stdout)
			return 0
		case "--version":
			fmt.Fprintf(stdout, "devcrew-report %s\n", config.Version)
			return 0
		}
	}
	if ctx == nil {
		fmt.Fprintln(stderr, "devcrew-report: runtime attachment is unavailable")
		return 1
	}
	if len(args) == 0 {
		writeInvalidCommand(stderr)
		return 2
	}
	if args[0] == "brief" {
		if len(args) != 1 {
			writeInvalidCommand(stderr)
			return 2
		}
		brief, err := readCommandBrief(ctx, config.Capability)
		if err != nil {
			writeRuntimeFailure(stderr)
			return 1
		}
		_, _ = io.WriteString(stdout, brief.Content)
		return 0
	}
	if args[0] == "acknowledge" {
		if len(args) != 1 {
			writeInvalidCommand(stderr)
			return 2
		}
		if config.Capability == nil || config.WorkingDirectory == nil {
			writeRuntimeFailure(stderr)
			return 1
		}
		workingDirectory, err := config.WorkingDirectory()
		if err != nil || config.Capability.Acknowledge(ctx, workingDirectory) != nil {
			writeRuntimeFailure(stderr)
			return 1
		}
		fmt.Fprintln(stdout, "acknowledged launch")
		return 0
	}

	parsed, ok := parseReportCommand(args)
	if !ok {
		writeInvalidCommand(stderr)
		return 2
	}
	brief, err := readCommandBrief(ctx, config.Capability)
	if err != nil {
		writeRuntimeFailure(stderr)
		return 1
	}
	if config.Clock == nil || config.NewLocalReportID == nil {
		writeRuntimeFailure(stderr)
		return 1
	}
	localReportID, err := config.NewLocalReportID()
	if err != nil {
		fmt.Fprintln(stderr, "devcrew-report: report preparation failed")
		return 1
	}
	observedAt := config.Clock().UTC()
	report := domain.WorkerReport{
		SchemaVersion: 1, LocalReportID: localReportID,
		BriefRevision: brief.Revision, BriefRevisionHash: brief.RevisionHash,
		Kind: parsed.kind, ExternalKey: parsed.key, Summary: parsed.summary,
		Details: parsed.details, WorkerObservedAt: &observedAt,
	}
	if err := report.Validate(); err != nil {
		writeInvalidCommand(stderr)
		return 2
	}
	receipt, err := config.Capability.Report(ctx, report)
	if err != nil {
		writeRuntimeFailure(stderr)
		return 1
	}
	fmt.Fprintf(stdout, "accepted %s at state %d\n", receipt.LocalReportID, receipt.StateVersion)
	return 0
}

type parsedReportCommand struct {
	kind    domain.WorkerReportKind
	key     string
	summary string
	details string
}

func parseReportCommand(args []string) (parsedReportCommand, bool) {
	if len(args) == 0 {
		return parsedReportCommand{}, false
	}
	set := flag.NewFlagSet(args[0], flag.ContinueOnError)
	set.SetOutput(io.Discard)
	var key, summary, question, artifact string
	switch args[0] {
	case "progress", "blocked", "paused", "failed":
		set.StringVar(&summary, "summary", "", "")
	case "decision":
		set.StringVar(&key, "key", "", "")
		set.StringVar(&question, "question", "", "")
	case "candidate-complete":
		set.StringVar(&summary, "summary", "", "")
		set.StringVar(&artifact, "artifact", "", "")
	case "resolved":
		set.StringVar(&key, "key", "", "")
		set.StringVar(&summary, "summary", "", "")
	default:
		return parsedReportCommand{}, false
	}
	if err := set.Parse(args[1:]); err != nil || set.NArg() != 0 {
		return parsedReportCommand{}, false
	}
	parsed := parsedReportCommand{key: key, summary: summary}
	switch args[0] {
	case "progress":
		parsed.kind = domain.ReportProgress
	case "decision":
		parsed.kind, parsed.summary = domain.ReportDecision, question
	case "blocked":
		parsed.kind = domain.ReportBlocked
	case "paused":
		parsed.kind = domain.ReportPaused
	case "candidate-complete":
		parsed.kind = domain.ReportCandidateComplete
		if artifact != "" {
			parsed.details = "artifact: " + artifact
		}
	case "failed":
		parsed.kind = domain.ReportFailed
	case "resolved":
		parsed.kind = domain.ReportResolution
	}
	if parsed.summary == "" || (parsed.kind == domain.ReportDecision || parsed.kind == domain.ReportResolution) && parsed.key == "" ||
		parsed.kind == domain.ReportCandidateComplete && artifact == "" {
		return parsedReportCommand{}, false
	}
	return parsed, true
}

func readCommandBrief(ctx context.Context, capability RuntimeCapability) (domain.WorkerBrief, error) {
	if capability == nil {
		return domain.WorkerBrief{}, errors.New("runtime capability is unavailable")
	}
	brief, err := capability.Brief(ctx)
	if err != nil || brief.Validate() != nil {
		return domain.WorkerBrief{}, errors.New("runtime brief is unavailable")
	}
	return brief, nil
}

func writeCommandUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: devcrew-report <command> [options]")
	fmt.Fprintln(output, "Commands:")
	fmt.Fprintln(output, "  acknowledge")
	fmt.Fprintln(output, "  brief")
	fmt.Fprintln(output, "  progress --summary TEXT")
	fmt.Fprintln(output, "  decision --key KEY --question TEXT")
	fmt.Fprintln(output, "  blocked --summary TEXT")
	fmt.Fprintln(output, "  paused --summary TEXT")
	fmt.Fprintln(output, "  candidate-complete --summary TEXT --artifact REF")
	fmt.Fprintln(output, "  failed --summary TEXT")
	fmt.Fprintln(output, "  resolved --key KEY --summary TEXT")
	fmt.Fprintln(output, "Reports accept only bounded content fields; task authority comes from the protected runtime attachment.")
}

func writeInvalidCommand(output io.Writer) {
	fmt.Fprintln(output, "devcrew-report: invalid command or report fields")
}

func writeRuntimeFailure(output io.Writer) {
	fmt.Fprintln(output, "devcrew-report: runtime attachment rejected the operation")
}
