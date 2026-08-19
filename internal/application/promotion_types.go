package application

import (
	"context"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ScoutPromotionSource is what a ship revision inherits from the scout it came
// from. It carries the scout's repository and base revision — the ship task must
// start from the same ground the investigation covered — and the digest of the
// evidence that justifies it. It carries no acceptance criteria or constraints:
// those describe what the ship task must achieve, which is a new decision the
// investigation informs rather than dictates.
type ScoutPromotionSource struct {
	ScoutTaskHandle string
	RepositoryID    string
	BaseRevision    string
	EvidenceDigest  string
}

// ScoutPromotionLink records that one ship task was created from one scout.
type ScoutPromotionLink struct {
	OperationID     string
	ScoutTaskHandle string
	ShipTaskHandle  string
	EvidenceDigest  string
	PromotedAt      time.Time
}

// PromoteScoutCommand creates a ship revision from one scout's investigation.
//
// It names the scout and supplies the ship contract. The repository and base
// revision are not accepted from the caller: they are inherited from the scout,
// so a promotion cannot quietly point the ship task at different ground than the
// investigation covered. Everything the ship task must achieve — its acceptance
// criteria, constraints, validation profile, delivery mode and worker — is a new
// decision the investigation informs rather than dictates, so it is stated here.
type PromoteScoutCommand struct {
	OperationID        string
	ServiceInstanceID  string
	ScoutTaskHandle    string
	AcceptanceCriteria []string
	Constraints        []string
	ValidationProfile  string
	DeliveryMode       domain.DeliveryMode
	WorkerProfileID    string
}

// ScoutPromotionStore is the durable authority one promotion needs beyond the
// ordinary preparation path.
type ScoutPromotionStore interface {
	ReadScoutPromotionSource(context.Context, string) (ScoutPromotionSource, error)
	// ReadScoutDecisionInventory reports the recorded inventory and whether one
	// exists. Promotion treats the investigation as a finished review, so it
	// refuses until somebody has stated that nothing is still open.
	ReadScoutDecisionInventory(context.Context, string) (ScoutDecisionInventory, bool, error)
	CommitScoutPromotionLink(context.Context, ScoutPromotionLink) error
}
