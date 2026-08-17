package reporter

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

type boundaryReportSink struct{}

func (boundaryReportSink) AcceptReport(context.Context, domain.AuthenticatedReport) (domain.ReportReceipt, error) {
	return domain.ReportReceipt{}, nil
}

func TestEndpointSubmitRejectsUnusableContextAndReport(t *testing.T) {
	const credential = "cred-0123456789abcdef0123456789abcdef"
	endpoint, err := NewEndpoint(EndpointConfig{
		TaskHandle: "task-0001", BriefRevision: 3, BriefRevisionHash: strings.Repeat("a", 64),
		Credential: credential, Sink: boundaryReportSink{},
	})
	if err != nil {
		t.Fatal(err)
	}
	//lint:ignore SA1012 This boundary test proves nil cannot reach report submission.
	if _, err := endpoint.submit(nil, credential, domain.WorkerReport{}); err == nil {
		t.Fatal("endpoint accepted a nil submission context")
	}
	if _, err := endpoint.submit(context.Background(), credential, domain.WorkerReport{}); !errors.Is(err, ErrInvalidReport) {
		t.Fatalf("endpoint.submit(invalid report) error = %v", err)
	}
}

func TestParseReportCommandRequiresArguments(t *testing.T) {
	if _, ok := parseReportCommand(nil); ok {
		t.Fatal("report command parsed without arguments")
	}
	if _, ok := parseReportCommand([]string{}); ok {
		t.Fatal("report command parsed from an empty argument list")
	}
}
