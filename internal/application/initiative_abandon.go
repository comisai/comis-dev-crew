package application

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// AbandonManagedRunGroupMember identifies one exact prepared group member.
type AbandonManagedRunGroupMember struct {
	ManagedRunID      string
	ExternalRunRef    string
	RegistrationNonce string
}

// AbandonManagedRunGroupCommand closes one unlaunched group preparation.
type AbandonManagedRunGroupCommand struct {
	OperationID       string
	ServiceInstanceID string
	ManagedRunGroupID string
	RegistrationNonce string
	Members           []AbandonManagedRunGroupMember
	Reason            AbandonReason
	Disposition       AbandonDisposition
}

// ManagedRunGroupAbandonmentMember is one validated store mutation member.
type ManagedRunGroupAbandonmentMember struct {
	ManagedRunID      string
	ExternalRunRef    string
	RegistrationNonce string
}

// ManagedRunGroupAbandonmentMutation is the durable group closure request.
type ManagedRunGroupAbandonmentMutation struct {
	ServiceInstanceID string
	ManagedRunGroupID string
	RegistrationNonce string
	Members           []ManagedRunGroupAbandonmentMember
	Reason            AbandonReason
	Disposition       AbandonDisposition
	OperationID       string
	SubjectDigest     string
	At                time.Time
}

// InitiativeAbandonmentResult preserves replay-stable per-member outcomes.
type InitiativeAbandonmentResult struct {
	Initiative  domain.DevelopmentInitiative
	Operation   domain.OperationRecord
	Members     []InitiativeActivationMemberResult
	Disposition AbandonDisposition
}

// InitiativeAbandonmentStore owns the atomic closure and exact replay result.
type InitiativeAbandonmentStore interface {
	ReplayInitiativeAbandonment(context.Context, string, string) (InitiativeAbandonmentResult, bool, error)
	CommitInitiativeAbandonment(context.Context, ManagedRunGroupAbandonmentMutation) (InitiativeAbandonmentResult, error)
}

// InitiativeAbandonmentConfig supplies the durable group closure boundaries.
type InitiativeAbandonmentConfig struct {
	Store InitiativeAbandonmentStore
	Clock Clock
}

// InitiativeAbandonments coordinates one exact prepared group closure.
type InitiativeAbandonments struct {
	store InitiativeAbandonmentStore
	clock Clock
}

// NewInitiativeAbandonments validates the group closure composition.
func NewInitiativeAbandonments(config InitiativeAbandonmentConfig) (*InitiativeAbandonments, error) {
	if config.Store == nil || config.Clock == nil {
		return nil, errors.New("create initiative abandonments: store and clock are required")
	}
	return &InitiativeAbandonments{store: config.Store, clock: config.Clock}, nil
}

// AbandonManagedRunGroup commits the exact member set before acknowledging it.
func (abandonments *InitiativeAbandonments) AbandonManagedRunGroup(
	ctx context.Context,
	command AbandonManagedRunGroupCommand,
) (InitiativeAbandonmentResult, error) {
	if err := validateManagedRunGroupAbandonment(ctx, command); err != nil {
		return InitiativeAbandonmentResult{}, err
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return InitiativeAbandonmentResult{}, mutationValidationFailure("group abandonment subject cannot be encoded")
	}
	result, found, err := abandonments.store.ReplayInitiativeAbandonment(
		ctx, command.OperationID, subjectDigest,
	)
	if err != nil {
		return InitiativeAbandonmentResult{}, mutationReplayFailure(err)
	}
	if found {
		return result, nil
	}
	members := make([]ManagedRunGroupAbandonmentMember, 0, len(command.Members))
	for _, member := range command.Members {
		members = append(members, ManagedRunGroupAbandonmentMember(member))
	}
	result, err = abandonments.store.CommitInitiativeAbandonment(ctx, ManagedRunGroupAbandonmentMutation{
		ServiceInstanceID: command.ServiceInstanceID, ManagedRunGroupID: command.ManagedRunGroupID,
		RegistrationNonce: command.RegistrationNonce, Members: members,
		Reason: command.Reason, Disposition: command.Disposition,
		OperationID: command.OperationID, SubjectDigest: subjectDigest, At: abandonments.clock(),
	})
	if err != nil {
		return InitiativeAbandonmentResult{}, mutationCommitFailure(err)
	}
	return result, nil
}

func validateManagedRunGroupAbandonment(ctx context.Context, command AbandonManagedRunGroupCommand) error {
	if err := validMutationContext(ctx); err != nil {
		return err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateAuthorityReference("serviceInstanceId", command.ServiceInstanceID) != nil ||
		domain.ValidateAuthorityReference("managedRunGroupId", command.ManagedRunGroupID) != nil ||
		!registrationNoncePattern.MatchString(command.RegistrationNonce) ||
		!command.Reason.valid() || !command.Disposition.valid() ||
		len(command.Members) == 0 || len(command.Members) > maximumInitiativeMembers {
		return mutationValidationFailure("group abandonment fields are invalid")
	}
	externalRefs := make([]string, 0, len(command.Members))
	managedRuns := make(map[string]struct{}, len(command.Members))
	nonces := map[string]struct{}{command.RegistrationNonce: {}}
	for _, member := range command.Members {
		if domain.ValidateAuthorityReference("managedRunId", member.ManagedRunID) != nil ||
			domain.ValidateTaskHandle(member.ExternalRunRef) != nil ||
			!registrationNoncePattern.MatchString(member.RegistrationNonce) {
			return mutationValidationFailure("group abandonment member fields are invalid")
		}
		if _, duplicate := managedRuns[member.ManagedRunID]; duplicate {
			return mutationValidationFailure("group abandonment managed-run identities must be unique")
		}
		if _, duplicate := nonces[member.RegistrationNonce]; duplicate {
			return mutationValidationFailure("group abandonment registration identities must be unique")
		}
		managedRuns[member.ManagedRunID] = struct{}{}
		nonces[member.RegistrationNonce] = struct{}{}
		externalRefs = append(externalRefs, member.ExternalRunRef)
	}
	sort.Strings(externalRefs)
	for index := 1; index < len(externalRefs); index++ {
		if externalRefs[index] == externalRefs[index-1] {
			return mutationValidationFailure("group abandonment external run references must be unique")
		}
	}
	return nil
}
