package comiswire

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
)

// ReleaseManagedRun implements the application cleanup port using the exact
// generated managed-run release exchange.
func (connection *ControlConnection) ReleaseManagedRun(
	ctx context.Context,
	request application.ManagedRunReleaseRequest,
) (application.ManagedRunReleaseReceipt, error) {
	if request.Disposition != application.ManagedRunReleaseReapSafe || request.ReleasedAt.IsZero() ||
		request.ReleasedAt.Location() != time.UTC || request.ReleasedAt.UnixMilli() < 0 {
		return application.ManagedRunReleaseReceipt{}, errors.New("release managed run: request is invalid")
	}
	result, err := connection.Release(ctx, ReleaseRequestParams{
		OperationID: OperationID(request.OperationID), ManagedRunID: ManagedRunID(request.ManagedRunID),
		WorkspaceLeaseID: WorkspaceLeaseID(request.WorkspaceLeaseID), Disposition: string(request.Disposition),
		ReleasedAtMs: request.ReleasedAt.UnixMilli(),
	})
	if err != nil {
		return application.ManagedRunReleaseReceipt{}, err
	}
	return application.ManagedRunReleaseReceipt{
		ManagedRunID: string(result.ManagedRunID), WorkspaceLeaseID: string(result.WorkspaceLeaseID),
		Disposition: application.ManagedRunReleaseDisposition(result.Disposition),
		ReleasedAt:  time.UnixMilli(result.ReleasedAtMs).UTC(),
		State:       application.ManagedRunReleaseState(result.State),
	}, nil
}

var _ application.ManagedRunReleaser = (*ControlConnection)(nil)
