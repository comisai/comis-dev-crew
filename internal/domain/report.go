package domain

import "time"

// WorkerReportKind is the closed E0 worker-to-service sparse-report vocabulary.
// Decision and resolution map to the generic Comis attention and resolution
// kinds only at the protocol adapter.
type WorkerReportKind string

const (
	ReportProgress          WorkerReportKind = "progress"
	ReportDecision          WorkerReportKind = "decision"
	ReportBlocked           WorkerReportKind = "blocked"
	ReportPaused            WorkerReportKind = "paused"
	ReportCandidateComplete WorkerReportKind = "candidate_complete"
	ReportFailed            WorkerReportKind = "failed"
	ReportResolution        WorkerReportKind = "resolution"
)

func (kind WorkerReportKind) valid() bool {
	switch kind {
	case ReportProgress, ReportDecision, ReportBlocked, ReportPaused,
		ReportCandidateComplete, ReportFailed, ReportResolution:
		return true
	default:
		return false
	}
}

// WorkerReport is untrusted worker content. It intentionally carries no task,
// run, service-instance, or caller identity; the authenticated endpoint derives
// that authority.
type WorkerReport struct {
	SchemaVersion     int
	LocalReportID     string
	BriefRevision     int64
	BriefRevisionHash string
	Kind              WorkerReportKind
	ExternalKey       string
	Summary           string
	Details           string
	WorkerObservedAt  *time.Time
}

// Validate enforces the bounded sparse vocabulary and brief pin.
func (report WorkerReport) Validate() error {
	if report.SchemaVersion != 1 {
		return &ValidationError{Field: "schemaVersion", Reason: "must equal 1"}
	}
	if err := validateOpaqueID("localReportId", report.LocalReportID); err != nil {
		return err
	}
	if report.BriefRevision < 1 {
		return &ValidationError{Field: "briefRevision", Reason: "must be positive"}
	}
	if err := validateSHA256("briefRevisionHash", report.BriefRevisionHash); err != nil {
		return err
	}
	if !report.Kind.valid() {
		return &ValidationError{Field: "kind", Reason: "must be a known sparse report kind"}
	}
	requiresKey := report.Kind == ReportDecision || report.Kind == ReportResolution
	if requiresKey {
		if err := validateOpaqueID("externalKey", report.ExternalKey); err != nil {
			return err
		}
	} else if report.ExternalKey != "" {
		return &ValidationError{Field: "externalKey", Reason: "is allowed only for decision or resolution reports"}
	}
	if err := validateSafeText("summary", report.Summary); err != nil {
		return err
	}
	if report.Details != "" {
		if err := validateBoundedSafeText("details", report.Details, 4096); err != nil {
			return err
		}
	}
	if report.WorkerObservedAt != nil {
		if report.WorkerObservedAt.IsZero() || report.WorkerObservedAt.Location() != time.UTC {
			return &ValidationError{Field: "workerObservedAt", Reason: "must be a nonzero UTC time when present"}
		}
	}
	return nil
}

// AuthenticatedReport joins untrusted sparse content to endpoint-derived task
// identity immediately before the canonical sink accepts it.
type AuthenticatedReport struct {
	TaskHandle string
	Report     WorkerReport
}

// ReportReceipt is the canonical sink acknowledgement returned to a reporter.
type ReportReceipt struct {
	TaskHandle    string
	LocalReportID string
	StateVersion  int64
	AcceptedAt    time.Time
	// PauseRequested tells the worker an operator has asked it to reach a safe
	// boundary and send a paused report. It rides the receipt rather than being
	// pushed into the worker's terminal: a request the worker pulls at a moment
	// it chose cannot land mid-edit, and needs no keystroke path to deliver.
	PauseRequested bool
	// Instruction is one operator steering instruction the worker has not seen
	// yet, empty when there is none. It rides the receipt for the same reason a
	// pause request does: the worker reads it at a moment it chose, so an
	// instruction cannot land in the middle of an edit and needs no keystroke
	// path to deliver.
	Instruction string
}

// AcceptedReport is the exact durable sparse-report evidence joined to the
// task state version produced by accepting it.
type AcceptedReport struct {
	TaskHandle    string
	Report        WorkerReport
	SubjectDigest string
	StateVersion  int64
	AcceptedAt    time.Time
}

// Validate enforces the persisted report identity and joined receipt fields.
func (accepted AcceptedReport) Validate() error {
	if err := validateOpaqueID("taskHandle", accepted.TaskHandle); err != nil {
		return err
	}
	if err := accepted.Report.Validate(); err != nil {
		return err
	}
	if err := validateSHA256("subjectDigest", accepted.SubjectDigest); err != nil {
		return err
	}
	if accepted.StateVersion < 1 {
		return &ValidationError{Field: "stateVersion", Reason: "must be positive"}
	}
	if accepted.AcceptedAt.IsZero() || accepted.AcceptedAt.Location() != time.UTC {
		return &ValidationError{Field: "acceptedAt", Reason: "must be nonzero UTC service time"}
	}
	return nil
}
