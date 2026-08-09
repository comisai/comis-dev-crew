package domain

import (
	"strings"
	"testing"
	"time"
)

func TestWorkerReportValidate_AcceptsClosedSparseVocabulary(t *testing.T) {
	for _, kind := range []WorkerReportKind{
		ReportProgress, ReportDecision, ReportBlocked, ReportPaused,
		ReportCandidateComplete, ReportFailed, ReportResolution,
	} {
		report := validWorkerReport(kind)
		if err := report.Validate(); err != nil {
			t.Fatalf("WorkerReport.Validate(%q) error = %v", kind, err)
		}
	}
}

func TestWorkerReportValidate_RejectsStaleUnsafeOrAmbiguousPayload(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*WorkerReport)
	}{
		{name: "schema", mutate: func(report *WorkerReport) { report.SchemaVersion = 2 }},
		{name: "local report id", mutate: func(report *WorkerReport) { report.LocalReportID = "../escape" }},
		{name: "brief revision", mutate: func(report *WorkerReport) { report.BriefRevision = 0 }},
		{name: "brief hash", mutate: func(report *WorkerReport) { report.BriefRevisionHash = "invalid" }},
		{name: "unknown kind", mutate: func(report *WorkerReport) { report.Kind = WorkerReportKind("finished") }},
		{name: "decision without key", mutate: func(report *WorkerReport) { report.ExternalKey = "" }},
		{name: "nondecision with key", mutate: func(report *WorkerReport) { report.Kind = ReportProgress }},
		{name: "unsafe summary", mutate: func(report *WorkerReport) { report.Summary = "unsafe\nsummary" }},
		{name: "oversized details", mutate: func(report *WorkerReport) { report.Details = strings.Repeat("x", 4097) }},
		{name: "non UTC observation", mutate: func(report *WorkerReport) {
			observed := report.WorkerObservedAt.In(time.FixedZone("offset", 3600))
			report.WorkerObservedAt = &observed
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report := validWorkerReport(ReportDecision)
			test.mutate(&report)
			if err := report.Validate(); err == nil {
				t.Fatal("WorkerReport.Validate() error = nil, want rejection")
			}
		})
	}
}

func validWorkerReport(kind WorkerReportKind) WorkerReport {
	observed := time.Date(2026, time.August, 9, 11, 0, 0, 0, time.UTC)
	report := WorkerReport{
		SchemaVersion:     1,
		LocalReportID:     "report-0001",
		BriefRevision:     1,
		BriefRevisionHash: strings.Repeat("a", 64),
		Kind:              kind,
		Summary:           "bounded report summary",
		Details:           "bounded detail",
		WorkerObservedAt:  &observed,
	}
	if kind == ReportDecision || kind == ReportResolution {
		report.ExternalKey = "decision-0001"
	}
	return report
}
