// Package reporter authenticates task-scoped sparse reports before they reach
// the canonical service-owned report sink.
package reporter

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const maximumReportBytes = 16 * 1024

var (
	// ErrUnauthorized means the supplied credential does not own this endpoint.
	ErrUnauthorized = errors.New("reporter credential is unauthorized")
	// ErrStaleBrief means the worker report does not bind the endpoint's exact brief.
	ErrStaleBrief = errors.New("worker report brief is stale")
	// ErrInvalidReport means strict report validation failed before sink access.
	ErrInvalidReport = errors.New("worker report is invalid")
	// ErrInvalidReceipt means the sink acknowledgement did not match the accepted report.
	ErrInvalidReceipt = errors.New("report receipt is invalid")
)

var credentialPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]{31,255}$`)

// ReportSink is the service-owned consumer seam. Implementations persist and
// deduplicate the authenticated task report before returning a receipt.
type ReportSink interface {
	AcceptReport(context.Context, domain.AuthenticatedReport) (domain.ReportReceipt, error)
}

// AuthenticationAuditor records one rejected worker credential.
//
// It is narrow on purpose. This boundary must be able to say that authority was
// presented and refused, and nothing more: the presented credential, the report
// body and the reason a correctly credentialed worker was wrong are all outside
// what an authentication trail should carry.
type AuthenticationAuditor interface {
	RecordReportAuthenticationFailure(context.Context, string) error
}

// EndpointConfig binds one credential and brief revision to exactly one task.
type EndpointConfig struct {
	TaskHandle        string
	BriefRevision     int64
	BriefRevisionHash string
	Credential        string
	Sink              ReportSink
	Auditor           AuthenticationAuditor
	// Logger is optional and Clock defaults to wall time. A deployment without
	// them accepts reports exactly as before, recording nothing.
	Logger application.BoundaryLogger
	Clock  func() time.Time
}

// Endpoint contains only a credential digest and immutable task scope.
type Endpoint struct {
	taskHandle        string
	briefRevision     int64
	briefRevisionHash string
	credentialHash    [sha256.Size]byte
	sink              ReportSink
	auditor           AuthenticationAuditor
	logger            application.BoundaryLogger
	clock             func() time.Time
}

// NewEndpoint validates and hashes one protected task reporter capability.
func NewEndpoint(config EndpointConfig) (*Endpoint, error) {
	if err := domain.ValidateTaskHandle(config.TaskHandle); err != nil {
		return nil, errors.New("create reporter endpoint: invalid task scope")
	}
	if config.BriefRevision < 1 {
		return nil, errors.New("create reporter endpoint: invalid brief revision")
	}
	if err := domain.ValidateBriefRevisionHash(config.BriefRevisionHash); err != nil {
		return nil, errors.New("create reporter endpoint: invalid brief hash")
	}
	if !credentialPattern.MatchString(config.Credential) {
		return nil, errors.New("create reporter endpoint: invalid credential shape")
	}
	if config.Sink == nil {
		return nil, errors.New("create reporter endpoint: report sink is required")
	}
	if config.Auditor == nil {
		return nil, errors.New("create reporter endpoint: authentication auditor is required")
	}
	clock := config.Clock
	if clock == nil {
		clock = time.Now
	}
	return &Endpoint{
		taskHandle: config.TaskHandle, briefRevision: config.BriefRevision,
		briefRevisionHash: config.BriefRevisionHash,
		credentialHash:    sha256.Sum256([]byte(config.Credential)), sink: config.Sink,
		auditor: config.Auditor, logger: config.Logger, clock: clock,
	}, nil
}

// Client is a task worker's narrow append-only reporter capability.
type Client struct {
	endpoint   *Endpoint
	credential string
}

// NewClient binds an endpoint to the protected credential presented by one worker.
func NewClient(endpoint *Endpoint, credential string) (*Client, error) {
	if endpoint == nil {
		return nil, errors.New("create reporter client: endpoint is required")
	}
	if !credentialPattern.MatchString(credential) {
		return nil, errors.New("create reporter client: invalid credential shape")
	}
	return &Client{endpoint: endpoint, credential: credential}, nil
}

// Report authenticates and appends one sparse task report.
func (client *Client) Report(ctx context.Context, report domain.WorkerReport) (domain.ReportReceipt, error) {
	if client == nil || client.endpoint == nil {
		return domain.ReportReceipt{}, errors.New("submit worker report: client is unavailable")
	}
	return client.endpoint.submit(ctx, client.credential, report)
}

// submit records one crossing of the worker boundary.
//
// Every report a confined worker sends arrives here, so this is where an
// operator can see that a worker is talking at all — and, on the failure paths,
// which of the four closed refusals it hit.
func (endpoint *Endpoint) submit(ctx context.Context, credential string, report domain.WorkerReport) (domain.ReportReceipt, error) {
	started := endpoint.clock()
	receipt, err := endpoint.accept(ctx, credential, report)
	record := application.BoundaryRecord{
		Boundary: application.BoundaryReporter, Operation: "submit_report",
		TaskHandle: endpoint.taskHandle, DurationMs: endpoint.clock().Sub(started).Milliseconds(),
		Outcome: application.BoundaryCompleted,
	}
	if err != nil {
		record.Outcome = application.BoundaryFailed
		record.ErrorKind, record.Hint = reportFailureClassification(err)
	}
	application.RecordBoundary(endpoint.logger, record)
	return receipt, err
}

// reportFailureClassification maps the endpoint's closed refusals onto the
// domain vocabulary, so an operator groups reporter failures the same way every
// other boundary is grouped.
func reportFailureClassification(err error) (domain.ErrorCode, string) {
	switch {
	case errors.Is(err, ErrUnauthorized):
		return domain.ErrorUnauthorized, "inspect the task reporter credential and its attachment"
	case errors.Is(err, ErrStaleBrief):
		return domain.ErrorConflict, "reconcile the worker brief revision before reporting again"
	case errors.Is(err, ErrInvalidReport):
		return domain.ErrorInvalidArgument, "send one bounded report matching the pinned schema"
	case errors.Is(err, ErrInvalidReceipt):
		return domain.ErrorInternal, "inspect the durable report sink and its receipt"
	default:
		return domain.ErrorUnavailable, "inspect the durable report sink"
	}
}

func (endpoint *Endpoint) accept(ctx context.Context, credential string, report domain.WorkerReport) (domain.ReportReceipt, error) {
	if ctx == nil {
		return domain.ReportReceipt{}, errors.New("submit worker report: context is required")
	}
	if err := ctx.Err(); err != nil {
		return domain.ReportReceipt{}, err
	}
	presentedHash := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare(presentedHash[:], endpoint.credentialHash[:]) != 1 {
		// The rejection stands whatever the trail does. A failed audit write is
		// reported beside it, never instead of it: an unauthorized report must
		// not become acceptable because the record of it could not be kept.
		if auditErr := endpoint.auditor.RecordReportAuthenticationFailure(ctx, endpoint.taskHandle); auditErr != nil {
			return domain.ReportReceipt{}, errors.Join(ErrUnauthorized, fmt.Errorf("audit rejected credential: %w", auditErr))
		}
		return domain.ReportReceipt{}, ErrUnauthorized
	}
	if err := report.Validate(); err != nil {
		return domain.ReportReceipt{}, fmt.Errorf("%w", ErrInvalidReport)
	}
	if report.BriefRevision != endpoint.briefRevision || report.BriefRevisionHash != endpoint.briefRevisionHash {
		return domain.ReportReceipt{}, ErrStaleBrief
	}
	encoded, err := json.Marshal(report)
	if err != nil || len(encoded) > maximumReportBytes {
		return domain.ReportReceipt{}, ErrInvalidReport
	}
	authenticated := domain.AuthenticatedReport{TaskHandle: endpoint.taskHandle, Report: report}
	receipt, err := endpoint.sink.AcceptReport(ctx, authenticated)
	if err != nil {
		return domain.ReportReceipt{}, &sinkFailure{cause: err}
	}
	if !validReceipt(receipt, authenticated) {
		return domain.ReportReceipt{}, ErrInvalidReceipt
	}
	return receipt, nil
}

func validReceipt(receipt domain.ReportReceipt, report domain.AuthenticatedReport) bool {
	return receipt.TaskHandle == report.TaskHandle &&
		receipt.LocalReportID == report.Report.LocalReportID &&
		receipt.StateVersion > 0 && !receipt.AcceptedAt.IsZero() && receipt.AcceptedAt.Location() == time.UTC
}

type sinkFailure struct{ cause error }

func (failure *sinkFailure) Error() string { return "report sink failed" }
func (failure *sinkFailure) Unwrap() error { return failure.cause }
