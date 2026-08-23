package application

import (
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type capturingBoundaryLogger struct {
	records []BoundaryRecord
}

func (logger *capturingBoundaryLogger) Record(record BoundaryRecord) {
	logger.records = append(logger.records, record)
}

// TestRecordBoundary_StripsFailureFieldsFromEverythingElse keeps a completion
// from carrying a stale kind or hint a caller left set. Reading those as a
// failure is how a healthy boundary starts looking broken in a dashboard.
func TestRecordBoundary_StripsFailureFieldsFromEverythingElse(t *testing.T) {
	for _, outcome := range []BoundaryOutcome{BoundaryCompleted, BoundaryStep} {
		logger := &capturingBoundaryLogger{}
		RecordBoundary(logger, BoundaryRecord{
			Boundary: BoundaryLocalAPI, Operation: "ListTasks", Outcome: outcome,
			ErrorKind: domain.ErrorInternal, Hint: "left over from a previous call",
			FailureCause:            BoundaryFailureDurableTaskContractInvalid,
			HostProjectionMismatch:  InitiativeHostMismatchStateCounts,
			ExpectedHostStateCounts: InitiativeHostStateCounts{Active: 1},
			ObservedHostStateCounts: InitiativeHostStateCounts{Waiting: 1},
		})
		if len(logger.records) != 1 {
			t.Fatalf("recorded %d, want 1", len(logger.records))
		}
		if logger.records[0].ErrorKind != "" || logger.records[0].Hint != "" || logger.records[0].FailureCause != "" ||
			logger.records[0].HostProjectionMismatch != "" ||
			logger.records[0].ExpectedHostStateCounts != (InitiativeHostStateCounts{}) ||
			logger.records[0].ObservedHostStateCounts != (InitiativeHostStateCounts{}) {
			t.Errorf("outcome %q kept failure fields: %#v", outcome, logger.records[0])
		}
	}
}

func TestRecordBoundary_KeepsFailureFields(t *testing.T) {
	logger := &capturingBoundaryLogger{}
	RecordBoundary(logger, BoundaryRecord{
		Boundary: BoundaryControl, Operation: "handshake", Outcome: BoundaryFailed,
		ErrorKind: domain.ErrorUnavailable, Hint: "inspect the control connection",
		FailureCause: BoundaryFailureDurableTaskContractInvalid,
	})
	if len(logger.records) != 1 {
		t.Fatalf("recorded %d, want 1", len(logger.records))
	}
	if logger.records[0].ErrorKind != domain.ErrorUnavailable || logger.records[0].Hint == "" ||
		logger.records[0].FailureCause != BoundaryFailureDurableTaskContractInvalid {
		t.Errorf("failure lost its classification: %#v", logger.records[0])
	}
}

func TestRecordBoundary_DropsUnknownFailureCauses(t *testing.T) {
	logger := &capturingBoundaryLogger{}
	RecordBoundary(logger, BoundaryRecord{
		Boundary: BoundaryLocalAPI, Operation: "PrepareInitiative", Outcome: BoundaryFailed,
		ErrorKind: domain.ErrorInternal, Hint: "inspect service health",
		FailureCause: BoundaryFailureCause("caller-supplied-detail"),
	})
	if len(logger.records) != 1 || logger.records[0].FailureCause != "" {
		t.Fatalf("records = %#v, want an unknown failure cause removed", logger.records)
	}
}

// TestRecordBoundary_RefusesAnUnnamedBoundary keeps the vocabulary countable. A
// record filed under a boundary nobody declared cannot be grouped by, so it is
// dropped rather than allowed to dilute the counts.
func TestRecordBoundary_RefusesAnUnnamedBoundary(t *testing.T) {
	logger := &capturingBoundaryLogger{}
	RecordBoundary(logger, BoundaryRecord{Boundary: Boundary("invented"), Outcome: BoundaryCompleted})
	if len(logger.records) != 0 {
		t.Fatalf("recorded %d, want none", len(logger.records))
	}
	for _, boundary := range []Boundary{BoundaryLocalAPI, BoundaryReporter, BoundaryControl} {
		if !boundary.Valid() {
			t.Errorf("declared boundary %q reports itself invalid", boundary)
		}
	}
}

// TestRecordBoundary_NormalisesAnImpossibleDuration guards a clock that moved
// backwards. A negative elapsed time would otherwise travel as a measurement.
func TestRecordBoundary_NormalisesAnImpossibleDuration(t *testing.T) {
	logger := &capturingBoundaryLogger{}
	RecordBoundary(logger, BoundaryRecord{
		Boundary: BoundaryLocalAPI, Operation: "ListTasks",
		Outcome: BoundaryCompleted, DurationMs: -5,
	})
	if len(logger.records) != 1 || logger.records[0].DurationMs != 0 {
		t.Fatalf("records = %#v, want a single non-negative duration", logger.records)
	}
}

// TestRecordBoundary_IsSafeWithoutALogger lets every call site record
// unconditionally instead of growing a branch for absent diagnostics.
func TestRecordBoundary_IsSafeWithoutALogger(t *testing.T) {
	RecordBoundary(nil, BoundaryRecord{Boundary: BoundaryLocalAPI, Outcome: BoundaryCompleted})
}
