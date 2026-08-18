package application

import (
	"context"
	"errors"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// PrimarySyncOutcome is the closed result of one primary-checkout sync.
type PrimarySyncOutcome string

const (
	PrimarySyncUpdated        PrimarySyncOutcome = "updated"
	PrimarySyncAlreadyCurrent PrimarySyncOutcome = "already_current"
	PrimarySyncRefused        PrimarySyncOutcome = "refused"
)

// PrimarySyncRefusal names the exact posture that refused the update, because
// each one has a different repair: committing or stashing an edit, deciding
// what becomes of local commits, or returning to the intended branch.
type PrimarySyncRefusal string

const (
	PrimarySyncRefusalDirty          PrimarySyncRefusal = "dirty_checkout"
	PrimarySyncRefusalDivergent      PrimarySyncRefusal = "divergent_history"
	PrimarySyncRefusalDetached       PrimarySyncRefusal = "detached_head"
	PrimarySyncRefusalNonDefault     PrimarySyncRefusal = "non_default_branch"
	PrimarySyncRefusalUpstreamAbsent PrimarySyncRefusal = "upstream_absent"
	PrimarySyncRefusalAmbiguous      PrimarySyncRefusal = "ambiguous_state"
)

// PrimarySyncCommand names the configured repository to synchronize. It carries
// no path: the checkout is resolved from operator configuration, so a caller
// cannot aim the update at a tree the deployment never approved.
type PrimarySyncCommand struct {
	OperationID  string
	RepositoryID string
}

// PrimarySyncReport is the bounded, content-free result of one synchronization.
type PrimarySyncReport struct {
	SchemaVersion int                `json:"schemaVersion"`
	RepositoryID  string             `json:"repositoryId"`
	Branch        string             `json:"branch,omitempty"`
	PreviousHead  string             `json:"previousHead,omitempty"`
	Head          string             `json:"head,omitempty"`
	Outcome       PrimarySyncOutcome `json:"outcome"`
	Refusal       PrimarySyncRefusal `json:"refusal,omitempty"`
}

// PrimarySynchronizer is the consumer-owned port for the one sanctioned
// primary-checkout mutation.
type PrimarySynchronizer interface {
	SynchronizePrimary(context.Context, PrimarySyncCommand) (PrimarySyncReport, error)
}

// PrimaryCheckoutConfig injects the synchronization adapter.
type PrimaryCheckoutConfig struct {
	Synchronizer PrimarySynchronizer
}

// PrimaryCheckouts coordinates synchronization of operator-configured primary
// checkouts. It owns no durable task state: a fast-forward changes no task, and
// re-running it against an already current checkout is inherently safe.
type PrimaryCheckouts struct {
	synchronizer PrimarySynchronizer
}

// NewPrimaryCheckouts binds the synchronization adapter.
func NewPrimaryCheckouts(config PrimaryCheckoutConfig) (*PrimaryCheckouts, error) {
	if config.Synchronizer == nil {
		return nil, errors.New("create primary checkouts: synchronizer is required")
	}
	return &PrimaryCheckouts{synchronizer: config.Synchronizer}, nil
}

// SyncPrimary fast-forwards one configured primary checkout, or reports the
// exact posture that refused.
//
// A refusal is a completed operation, not a failure: the checkout was inspected
// and found unfit to advance, which is an answer the operator acts on. An
// adapter failure stays a failure — telling somebody their checkout is dirty
// when Git was actually unreachable sends them to repair the wrong thing.
func (checkouts *PrimaryCheckouts) SyncPrimary(
	ctx context.Context,
	command PrimarySyncCommand,
) (PrimarySyncReport, error) {
	if err := validMutationContext(ctx); err != nil {
		return PrimarySyncReport{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateRepositoryID(command.RepositoryID) != nil {
		return PrimarySyncReport{}, mutationValidationFailure("primary synchronization identity is invalid")
	}
	report, err := checkouts.synchronizer.SynchronizePrimary(ctx, command)
	if err != nil {
		return PrimarySyncReport{}, err
	}
	report.SchemaVersion, report.RepositoryID = 1, command.RepositoryID
	return report, nil
}
