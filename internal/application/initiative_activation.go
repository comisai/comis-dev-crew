package application

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

// ActivateManagedRunGroupMember is one exact host-owned member binding.
type ActivateManagedRunGroupMember struct {
	ManagedRunID          string
	ExternalRunRef        string
	RegistrationNonce     string
	WorkspaceLeaseID      string
	ExecutionAttachmentID string
	AttachmentTargetName  string
}

// ActivateManagedRunGroupCommand carries the complete same-scope host binding.
type ActivateManagedRunGroupCommand struct {
	OperationID       string
	ServiceInstanceID string
	ManagedRunGroupID string
	RegistrationNonce string
	Members           []ActivateManagedRunGroupMember
}

// ManagedRunGroupActivationMember is one validated store mutation member.
type ManagedRunGroupActivationMember struct {
	ExternalRunRef        string
	RegistrationNonce     string
	Binding               domain.TaskBinding
	ExecutionAttachmentID string
	AttachmentTargetName  string
}

// ManagedRunGroupActivationMutation is the all-or-none durable group join.
type ManagedRunGroupActivationMutation struct {
	ServiceInstanceID string
	ManagedRunGroupID string
	RegistrationNonce string
	Members           []ManagedRunGroupActivationMember
	OperationID       string
	SubjectDigest     string
	At                time.Time
}

// InitiativeActivationOutcome is the closed member result vocabulary.
type InitiativeActivationOutcome string

const (
	InitiativeActivationCompleted    InitiativeActivationOutcome = "completed"
	InitiativeActivationRejected     InitiativeActivationOutcome = "rejected"
	InitiativeActivationUnknown      InitiativeActivationOutcome = "unknown"
	InitiativeActivationNotAttempted InitiativeActivationOutcome = "not_attempted"
)

// InitiativeActivationMemberResult reports one host member outcome.
type InitiativeActivationMemberResult struct {
	ManagedRunID string
	Outcome      InitiativeActivationOutcome
}

// InitiativeActivationResult joins the committed group and attachment outcomes.
type InitiativeActivationResult struct {
	Initiative domain.DevelopmentInitiative
	Tasks      []domain.Task
	Operation  domain.OperationRecord
	Members    []InitiativeActivationMemberResult
}

// InitiativeActivationStore owns the atomic group join and its visible posture.
type InitiativeActivationStore interface {
	ReplayInitiativeActivation(context.Context, string, string) (InitiativeActivationResult, bool, error)
	CommitInitiativeActivation(context.Context, ManagedRunGroupActivationMutation) (InitiativeActivationResult, error)
	SetInitiativeActivationState(context.Context, string, domain.InitiativeState, time.Time) (domain.DevelopmentInitiative, error)
}

// InitiativeActivationConfig supplies the reviewed binding boundaries.
type InitiativeActivationConfig struct {
	Store              InitiativeActivationStore
	RuntimeAttachments RuntimeAttachmentCoordinator
	Acknowledger       WorkerLaunchAcknowledger
	Clock              Clock
}

// InitiativeActivations coordinates one same-scope group activation.
type InitiativeActivations struct {
	store        InitiativeActivationStore
	attachments  RuntimeAttachmentCoordinator
	acknowledger WorkerLaunchAcknowledger
	clock        Clock
}

// NewInitiativeActivations validates the group activation composition.
func NewInitiativeActivations(config InitiativeActivationConfig) (*InitiativeActivations, error) {
	if config.Store == nil || config.RuntimeAttachments == nil || config.Acknowledger == nil || config.Clock == nil {
		return nil, errors.New("create initiative activations: store, runtime attachments, acknowledger, and clock are required")
	}
	return &InitiativeActivations{
		store: config.Store, attachments: config.RuntimeAttachments,
		acknowledger: config.Acknowledger, clock: config.Clock,
	}, nil
}

// ActivateManagedRunGroup commits every host binding before touching runtime
// attachments. A local partial attachment bind is returned member-by-member and
// leaves the initiative unknown, so no scheduler can treat it as launchable.
func (activations *InitiativeActivations) ActivateManagedRunGroup(
	ctx context.Context,
	command ActivateManagedRunGroupCommand,
) (InitiativeActivationResult, error) {
	if err := validateManagedRunGroupActivation(ctx, command); err != nil {
		return InitiativeActivationResult{}, err
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return InitiativeActivationResult{}, mutationValidationFailure("group activation subject cannot be encoded")
	}
	result, found, err := activations.store.ReplayInitiativeActivation(ctx, command.OperationID, subjectDigest)
	if err != nil {
		return InitiativeActivationResult{}, mutationReplayFailure(err)
	}
	if !found {
		members := make([]ManagedRunGroupActivationMember, 0, len(command.Members))
		for _, member := range command.Members {
			members = append(members, ManagedRunGroupActivationMember{
				ExternalRunRef: member.ExternalRunRef, RegistrationNonce: member.RegistrationNonce,
				Binding:               domain.TaskBinding{ManagedRunID: member.ManagedRunID, WorkspaceLeaseID: member.WorkspaceLeaseID},
				ExecutionAttachmentID: member.ExecutionAttachmentID,
				AttachmentTargetName:  member.AttachmentTargetName,
			})
		}
		result, err = activations.store.CommitInitiativeActivation(ctx, ManagedRunGroupActivationMutation{
			ServiceInstanceID: command.ServiceInstanceID, ManagedRunGroupID: command.ManagedRunGroupID,
			RegistrationNonce: command.RegistrationNonce, Members: members,
			OperationID: command.OperationID, SubjectDigest: subjectDigest, At: activations.clock(),
		})
		if err != nil {
			return InitiativeActivationResult{}, mutationCommitFailure(err)
		}
	}

	tasks := make(map[string]domain.Task, len(result.Tasks))
	for _, task := range result.Tasks {
		tasks[task.Handle] = task
	}
	result.Members = make([]InitiativeActivationMemberResult, 0, len(command.Members))
	partial := false
	for _, member := range command.Members {
		task, exists := tasks[member.ExternalRunRef]
		if !exists || task.ManagedRunID != member.ManagedRunID {
			return InitiativeActivationResult{}, mutationValidationFailure("committed group member result is incomplete")
		}
		outcome := InitiativeActivationCompleted
		launchOperationID, operationErr := RuntimeLaunchAcknowledgementOperationID(member.ExternalRunRef)
		if operationErr != nil {
			return InitiativeActivationResult{}, mutationValidationFailure("runtime launch acknowledgement identity is invalid")
		}
		bindingErr := activations.attachments.BindRuntimeAttachment(ctx, RuntimeAttachmentBindingRequest{
			TaskHandle: member.ExternalRunRef, ManagedRunID: member.ManagedRunID,
			WorkspaceLeaseID: member.WorkspaceLeaseID, ExecutionAttachmentID: member.ExecutionAttachmentID,
			AttachmentTargetName: member.AttachmentTargetName, LaunchOperationID: launchOperationID,
			Acknowledger: activations.acknowledger,
		})
		if bindingErr != nil {
			outcome = InitiativeActivationUnknown
			partial = true
		}
		result.Members = append(result.Members, InitiativeActivationMemberResult{
			ManagedRunID: member.ManagedRunID, Outcome: outcome,
		})
	}
	desiredState := domain.InitiativeActive
	if partial {
		desiredState = domain.InitiativeUnknown
	}
	if result.Initiative.State != desiredState {
		updated, err := activations.store.SetInitiativeActivationState(
			ctx, command.ManagedRunGroupID, desiredState, activations.clock(),
		)
		if err != nil {
			return InitiativeActivationResult{}, mutationCommitFailure(err)
		}
		result.Initiative = updated
	}
	return result, nil
}

func validateManagedRunGroupActivation(ctx context.Context, command ActivateManagedRunGroupCommand) error {
	if err := validMutationContext(ctx); err != nil {
		return err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateAuthorityReference("serviceInstanceId", command.ServiceInstanceID) != nil ||
		domain.ValidateAuthorityReference("managedRunGroupId", command.ManagedRunGroupID) != nil ||
		!registrationNoncePattern.MatchString(command.RegistrationNonce) ||
		len(command.Members) == 0 || len(command.Members) > maximumInitiativeMembers {
		return mutationValidationFailure("group activation fields are invalid")
	}
	externalRefs := make([]string, 0, len(command.Members))
	managedRuns := make(map[string]struct{}, len(command.Members))
	nonces := map[string]struct{}{command.RegistrationNonce: {}}
	for _, member := range command.Members {
		binding := domain.TaskBinding{ManagedRunID: member.ManagedRunID, WorkspaceLeaseID: member.WorkspaceLeaseID}
		if domain.ValidateTaskHandle(member.ExternalRunRef) != nil || binding.Validate() != nil ||
			!registrationNoncePattern.MatchString(member.RegistrationNonce) ||
			domain.ValidateAuthorityReference("executionAttachmentId", member.ExecutionAttachmentID) != nil ||
			domain.ValidateAttachmentTargetName(member.AttachmentTargetName) != nil {
			return mutationValidationFailure("group member activation fields are invalid")
		}
		if _, duplicate := managedRuns[member.ManagedRunID]; duplicate {
			return mutationValidationFailure("group managed-run identities must be unique")
		}
		if _, duplicate := nonces[member.RegistrationNonce]; duplicate {
			return mutationValidationFailure("group registration identities must be unique")
		}
		managedRuns[member.ManagedRunID] = struct{}{}
		nonces[member.RegistrationNonce] = struct{}{}
		externalRefs = append(externalRefs, member.ExternalRunRef)
	}
	sort.Strings(externalRefs)
	for index := 1; index < len(externalRefs); index++ {
		if externalRefs[index] == externalRefs[index-1] {
			return mutationValidationFailure("group external run references must be unique")
		}
	}
	return nil
}
