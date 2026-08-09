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

// EndpointConfig binds one credential and brief revision to exactly one task.
type EndpointConfig struct {
	TaskHandle        string
	BriefRevision     int64
	BriefRevisionHash string
	Credential        string
	Sink              ReportSink
}

// Endpoint contains only a credential digest and immutable task scope.
type Endpoint struct {
	taskHandle        string
	briefRevision     int64
	briefRevisionHash string
	credentialHash    [sha256.Size]byte
	sink              ReportSink
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
	return &Endpoint{
		taskHandle: config.TaskHandle, briefRevision: config.BriefRevision,
		briefRevisionHash: config.BriefRevisionHash,
		credentialHash:    sha256.Sum256([]byte(config.Credential)), sink: config.Sink,
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

func (endpoint *Endpoint) submit(ctx context.Context, credential string, report domain.WorkerReport) (domain.ReportReceipt, error) {
	if ctx == nil {
		return domain.ReportReceipt{}, errors.New("submit worker report: context is required")
	}
	if err := ctx.Err(); err != nil {
		return domain.ReportReceipt{}, err
	}
	presentedHash := sha256.Sum256([]byte(credential))
	if subtle.ConstantTimeCompare(presentedHash[:], endpoint.credentialHash[:]) != 1 {
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
