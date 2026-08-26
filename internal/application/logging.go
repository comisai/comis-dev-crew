package application

import "github.com/comisai/comis-dev-crew/internal/domain"

// Boundary names one place work crosses into or out of this service.
//
// It is closed because the point of the record is to be countable: an operator
// asking which boundary is slow or failing needs a fixed vocabulary to group by,
// and an open one would make two spellings of the same seam look like two seams.
type Boundary string

const (
	// BoundaryLocalAPI is the owner-only socket every console and model call crosses.
	BoundaryLocalAPI Boundary = "local_api"
	// BoundaryReporter is the per-task endpoint a confined worker reports through.
	BoundaryReporter Boundary = "reporter"
	// BoundaryControl is the authenticated Comis control connection.
	BoundaryControl Boundary = "control"
)

// Valid reports whether the boundary is one this service can name.
func (boundary Boundary) Valid() bool {
	switch boundary {
	case BoundaryLocalAPI, BoundaryReporter, BoundaryControl:
		return true
	default:
		return false
	}
}

// BoundaryOutcome is the closed result of one boundary crossing.
type BoundaryOutcome string

const (
	// BoundaryCompleted is a crossing that produced an answer, including a
	// refusal the caller asked for. It is the ordinary case.
	BoundaryCompleted BoundaryOutcome = "completed"
	// BoundaryFailed is a crossing that could not produce one.
	BoundaryFailed BoundaryOutcome = "failed"
	// BoundaryStep is an intermediate stage inside one crossing, recorded so a
	// call that never completed can still be located.
	BoundaryStep BoundaryOutcome = "step"
)

// BoundaryFailureCause is a content-free diagnosis for a known failure class.
// It stays closed so boundary logs cannot acquire task text, paths, arguments,
// or dependency messages while still naming the subsystem an operator must
// inspect.
type BoundaryFailureCause string

const (
	BoundaryFailureDurableTaskContractInvalid       BoundaryFailureCause = "durable_task_contract_invalid"
	BoundaryFailureControlHandshakePrecondition     BoundaryFailureCause = "control_handshake_precondition_failed"
	BoundaryFailureControlConnectionUnavailable     BoundaryFailureCause = "control_connection_unavailable"
	BoundaryFailureInitiativeHostProjectionMismatch BoundaryFailureCause = "initiative_host_projection_mismatch"
)

func (cause BoundaryFailureCause) valid() bool {
	switch cause {
	case "", BoundaryFailureDurableTaskContractInvalid, BoundaryFailureControlHandshakePrecondition,
		BoundaryFailureControlConnectionUnavailable, BoundaryFailureInitiativeHostProjectionMismatch:
		return true
	default:
		return false
	}
}

// BoundaryRecord is everything this service will say about one crossing.
//
// It is a closed struct rather than a set of caller-supplied key-value pairs,
// and that is the whole design. A logger taking free-form attributes makes
// "never log a brief, objective, report, diff, path, argument or credential" a
// rule every future call site must remember; a logger taking this struct makes
// it a property of the type. There is no field a payload could occupy.
type BoundaryRecord struct {
	Boundary Boundary `json:"boundary"`
	// Operation is the closed method or fixed stage name, never caller text.
	Operation string `json:"operation"`
	// OperationID and TaskHandle are opaque service-owned identities.
	OperationID string `json:"operationId,omitempty"`
	TaskHandle  string `json:"taskHandle,omitempty"`
	// Initiative and group identities are opaque durable handles. Host state
	// counts are content-free and appear only for startup projection diagnosis.
	InitiativeHandle        string                           `json:"initiativeHandle,omitempty"`
	ManagedRunGroupID       string                           `json:"managedRunGroupId,omitempty"`
	AttemptCount            int                              `json:"attemptCount,omitempty"`
	HostProjectionMismatch  InitiativeHostProjectionMismatch `json:"hostProjectionMismatch,omitempty"`
	ExpectedHostStateCounts InitiativeHostStateCounts        `json:"expectedHostStateCounts,omitempty"`
	ObservedHostStateCounts InitiativeHostStateCounts        `json:"observedHostStateCounts,omitempty"`
	DurationMs              int64                            `json:"durationMs"`
	Outcome                 BoundaryOutcome                  `json:"outcome"`
	// ErrorKind and Hint are present only on a failure. Both come from the
	// closed domain failure vocabulary, so neither can carry untrusted text.
	ErrorKind    domain.ErrorCode     `json:"errorKind,omitempty"`
	Hint         string               `json:"hint,omitempty"`
	FailureCause BoundaryFailureCause `json:"failureCause,omitempty"`
}

// BoundaryLogger receives one record per crossing.
//
// It returns nothing on purpose. Recording is diagnostic, and a boundary that
// could fail because its own log write failed would trade the work the service
// exists to do for the record of having done it.
type BoundaryLogger interface {
	Record(BoundaryRecord)
}

// RecordBoundary emits one record when a logger is configured.
//
// A nil logger is an ordinary deployment that records nothing, so every call
// site can log unconditionally instead of guarding, and no boundary acquires a
// branch that exists only for the absence of diagnostics.
func RecordBoundary(logger BoundaryLogger, record BoundaryRecord) {
	if logger == nil || !record.Boundary.Valid() {
		return
	}
	if record.Outcome != BoundaryFailed {
		record.ErrorKind, record.Hint, record.FailureCause = "", "", ""
		record.HostProjectionMismatch = ""
		record.ExpectedHostStateCounts = InitiativeHostStateCounts{}
		record.ObservedHostStateCounts = InitiativeHostStateCounts{}
	} else if !record.FailureCause.valid() {
		record.FailureCause = ""
	}
	if record.DurationMs < 0 {
		record.DurationMs = 0
	}
	if record.AttemptCount < 0 {
		record.AttemptCount = 0
	}
	if record.Outcome == BoundaryFailed && !record.HostProjectionMismatch.valid() {
		record.HostProjectionMismatch = ""
	}
	logger.Record(record)
}
