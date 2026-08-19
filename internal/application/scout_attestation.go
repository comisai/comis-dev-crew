package application

import (
	"context"
	"errors"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// MaximumAttestedDecisionKeys bounds one recorded inventory.
const MaximumAttestedDecisionKeys = 64

// ScoutAttestationFinding is the closed judgement a liaison records after
// inventorying a scout's whole reviewed surface.
//
// It is a discriminator rather than an inferred emptiness on purpose. A request
// that named no finding would otherwise be indistinguishable from one that
// found nothing, and reading silence as "nothing was open" is exactly the
// mistake this record exists to prevent: a buried question in a report is then
// erased along with the worktree that held it.
type ScoutAttestationFinding string

const (
	// ScoutAttestationOpenDecisions enumerates the decisions still awaiting a
	// human. It requires at least one key.
	ScoutAttestationOpenDecisions ScoutAttestationFinding = "open_decisions"
	// ScoutAttestationNoOpenDecisions is the positive statement that the
	// reviewed surface holds no unresolved human decision. It requires no keys.
	ScoutAttestationNoOpenDecisions ScoutAttestationFinding = "no_open_decisions"
)

// AttestScoutDecisionsCommand records one liaison inventory.
//
// Only a model can inventory decisions from prose, so the service never derives
// this: it stores the judgement it was given, and refuses one it cannot read as
// a complete statement.
type AttestScoutDecisionsCommand struct {
	OperationID      string
	TaskHandle       string
	Finding          ScoutAttestationFinding
	OpenDecisionKeys []string
}

// ScoutDecisionAttestationMutation is the durable half of one recorded
// inventory.
type ScoutDecisionAttestationMutation struct {
	OperationID      string
	SubjectDigest    string
	TaskHandle       string
	Finding          ScoutAttestationFinding
	OpenDecisionKeys []string
	At               time.Time
}

// ScoutDecisionInventory is one recorded inventory read back.
//
// Its absence is not an empty inventory. A caller that cannot find a record has
// learned that nobody has looked, which is a different fact from somebody
// having looked and found nothing.
type ScoutDecisionInventory struct {
	TaskHandle       string
	Finding          ScoutAttestationFinding
	OpenDecisionKeys []string
	AttestedAt       time.Time
}

// ClearsReview reports whether this inventory leaves the surface reviewable.
func (inventory ScoutDecisionInventory) ClearsReview() bool {
	return inventory.Finding == ScoutAttestationNoOpenDecisions
}

// ErrScoutReviewUnattested identifies a scout whose reviewed surface nobody has
// inventoried, or whose inventory still names open decisions.
var ErrScoutReviewUnattested = errors.New("scout decision inventory is missing or unresolved")

// ProveScoutReviewCleared refuses to treat an investigation as reviewed unless
// a recorded inventory says so.
//
// Absence and an unresolved finding fail the same way on purpose: acting on an
// investigation nobody inventoried carries its authority without its unanswered
// parts, and so does acting on one whose questions are still open.
func ProveScoutReviewCleared(inventory ScoutDecisionInventory, found bool) error {
	if !found || !inventory.ClearsReview() {
		return ErrScoutReviewUnattested
	}
	return nil
}

// ScoutAttestationStore persists and reads recorded inventories.
type ScoutAttestationStore interface {
	CommitScoutDecisionAttestation(context.Context, ScoutDecisionAttestationMutation) (MutationResult, error)
	ReadScoutDecisionInventory(context.Context, string) (ScoutDecisionInventory, bool, error)
}

// ScoutReviewConfig injects the durable attestation surface.
type ScoutReviewConfig struct {
	Store ScoutAttestationStore
	Clock Clock
}

// ScoutReviews records the liaison's decision inventory for a scout.
type ScoutReviews struct {
	store ScoutAttestationStore
	clock Clock
}

// NewScoutReviews binds the attestation store and clock.
func NewScoutReviews(config ScoutReviewConfig) (*ScoutReviews, error) {
	if config.Store == nil || config.Clock == nil {
		return nil, errors.New("create scout reviews: store and clock are required")
	}
	return &ScoutReviews{store: config.Store, clock: config.Clock}, nil
}

// AttestScoutDecisions records one liaison inventory of a scout's still-open
// human decisions.
func (reviews *ScoutReviews) AttestScoutDecisions(
	ctx context.Context,
	command AttestScoutDecisionsCommand,
) (MutationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return MutationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.TaskHandle) != nil {
		return MutationResult{}, mutationValidationFailure("attestation identity is invalid")
	}
	keys, err := validateAttestedInventory(command.Finding, command.OpenDecisionKeys)
	if err != nil {
		return MutationResult{}, err
	}
	digest, err := digestMutationSubject(command)
	if err != nil {
		return MutationResult{}, mutationValidationFailure("attestation subject cannot be encoded")
	}
	return reviews.store.CommitScoutDecisionAttestation(ctx, ScoutDecisionAttestationMutation{
		OperationID: command.OperationID, SubjectDigest: digest, TaskHandle: command.TaskHandle,
		Finding: command.Finding, OpenDecisionKeys: keys, At: reviews.clock(),
	})
}

// validateAttestedInventory holds each finding to the shape that makes it a
// complete statement, so neither can be reached by omission.
func validateAttestedInventory(
	finding ScoutAttestationFinding,
	keys []string,
) ([]string, error) {
	switch finding {
	case ScoutAttestationNoOpenDecisions:
		if len(keys) != 0 {
			return nil, mutationValidationFailure("a cleared inventory cannot also name open decisions")
		}
		return nil, nil
	case ScoutAttestationOpenDecisions:
		if len(keys) == 0 {
			return nil, mutationValidationFailure("an inventory of open decisions names none")
		}
		if len(keys) > MaximumAttestedDecisionKeys {
			return nil, mutationValidationFailure("attested inventory exceeds its bound")
		}
		seen := make(map[string]struct{}, len(keys))
		recorded := make([]string, 0, len(keys))
		for _, key := range keys {
			if domain.ValidateDecisionKey(key) != nil {
				return nil, mutationValidationFailure("attested decision key is invalid")
			}
			if _, duplicate := seen[key]; duplicate {
				return nil, mutationValidationFailure("attested inventory repeats a decision key")
			}
			seen[key] = struct{}{}
			recorded = append(recorded, key)
		}
		return recorded, nil
	default:
		return nil, mutationValidationFailure("attestation states no closed finding")
	}
}
