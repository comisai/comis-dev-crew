package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandPrepareInitiative = "PrepareInitiative"
const maximumInitiativeMembers = 16

// PrepareInitiativeTaskContract is one immutable member task contract. Its base
// revision and repository come from the containing component and frozen base set.
type PrepareInitiativeTaskContract struct {
	Shape              domain.TaskShape
	AcceptanceCriteria []string
	Constraints        []string
	ConsumedContracts  []domain.PinnedContract
	ValidationProfile  string
	DeliveryMode       domain.DeliveryMode
	WorkerProfileID    string
}

// PrepareInitiativeTask gives one caller-local reference to a task contract.
// The service replaces the reference with a minted durable task handle before
// any workspace is allocated.
type PrepareInitiativeTask struct {
	TaskRef  string
	Contract PrepareInitiativeTaskContract
}

// PrepareInitiativeComponent groups task contracts under one repository responsibility.
type PrepareInitiativeComponent struct {
	ComponentHandle   string
	RepositoryID      string
	ResponsibilityRef string
	Tasks             []PrepareInitiativeTask
}

// PrepareInitiativeEdge names dependencies using caller-local task references.
type PrepareInitiativeEdge struct {
	FromTaskRef          string
	ToTaskRef            string
	Kind                 domain.InitiativeEdgeKind
	RequiredArtifactKind domain.ContractArtifactKind
}

// PrepareInitiativeCommand is the complete graph and immutable member contract set.
type PrepareInitiativeCommand struct {
	OperationID          string
	ServiceInstanceID    string
	TitleRef             string
	BaseRevisionSet      []domain.InitiativeBaseRevision
	Components           []PrepareInitiativeComponent
	Edges                []PrepareInitiativeEdge
	ContractArtifacts    []string
	IntegrationPolicyID  string
	IntegrationOwnerTask string
}

// ManagedRunGroupPreparation is private adapter metadata for one unbound group.
// It carries no host authority: Comis still mints the group and every member run.
type ManagedRunGroupPreparation struct {
	ExternalGroupRef  string
	RegistrationNonce string
	Members           []ManagedRunPreparation
	ExpiresAt         time.Time
}

// PreparedInitiativeMember joins one durable task, its private preparation, and
// the stable child operation used by cleanup and replay queries.
type PreparedInitiativeMember struct {
	Task          domain.Task
	Preparation   ManagedRunPreparation
	OperationID   string
	SubjectDigest string
}

// PreparedInitiativeMutation is committed as one store transaction after every
// reversible workspace and runtime attachment has been prepared.
type PreparedInitiativeMutation struct {
	Initiative             domain.DevelopmentInitiative
	Members                []PreparedInitiativeMember
	GroupRegistrationNonce string
	GroupExpiresAt         time.Time
	OperationID            string
	SubjectDigest          string
	At                     time.Time
}

// InitiativePreparationResult is the private canonical result used for exact replay.
type InitiativePreparationResult struct {
	Initiative  domain.DevelopmentInitiative
	Tasks       []domain.Task
	Preparation ManagedRunGroupPreparation
	Operation   domain.OperationRecord
}

// InitiativeMutationStore owns initiative replay, preparation intents, and the
// all-or-none initiative/member commit.
type InitiativeMutationStore interface {
	ReplayInitiativePreparation(context.Context, string, string) (InitiativePreparationResult, bool, error)
	RecordTaskPreparationIntent(context.Context, TaskPreparationIntent) (TaskPreparationIntent, error)
	CommitPreparedInitiative(context.Context, PreparedInitiativeMutation) (InitiativePreparationResult, error)
}

// InitiativeMutationConfig supplies the existing reviewed preparation boundaries.
type InitiativeMutationConfig struct {
	Store              InitiativeMutationStore
	Repositories       RepositoryCatalog
	WorkerProfiles     WorkerProfileValidator
	ValidationProfiles ValidationProfileValidator
	Workspaces         WorkspacePreparer
	RuntimeAttachments RuntimeAttachmentCoordinator
	TaskIDs            TaskIDSource
	RegistrationNonces RegistrationNonceSource
	PreparationTTL     time.Duration
	Clock              Clock
}

// InitiativeMutations coordinates one graph preparation without launch authority.
type InitiativeMutations struct {
	store              InitiativeMutationStore
	repositories       RepositoryCatalog
	workerProfiles     WorkerProfileValidator
	validationProfiles ValidationProfileValidator
	workspaces         WorkspacePreparer
	attachments        RuntimeAttachmentCoordinator
	taskIDs            TaskIDSource
	nonces             RegistrationNonceSource
	preparationTTL     time.Duration
	clock              Clock
}

// NewInitiativeMutations creates the multi-component preparation coordinator.
func NewInitiativeMutations(config InitiativeMutationConfig) (*InitiativeMutations, error) {
	if config.Store == nil || config.Repositories == nil || config.WorkerProfiles == nil ||
		config.ValidationProfiles == nil || config.Workspaces == nil || config.RuntimeAttachments == nil ||
		config.TaskIDs == nil || config.RegistrationNonces == nil || config.Clock == nil {
		return nil, errors.New("create initiative mutations: store, repositories, profiles, workspaces, runtime attachments, task IDs, registration nonces, and clock are required")
	}
	if config.PreparationTTL <= 0 || config.PreparationTTL > 24*time.Hour {
		return nil, errors.New("create initiative mutations: preparation TTL must be within 24 hours")
	}
	return &InitiativeMutations{
		store: config.Store, repositories: config.Repositories,
		workerProfiles: config.WorkerProfiles, validationProfiles: config.ValidationProfiles,
		workspaces: config.Workspaces, attachments: config.RuntimeAttachments,
		taskIDs: config.TaskIDs, nonces: config.RegistrationNonces,
		preparationTTL: config.PreparationTTL, clock: config.Clock,
	}, nil
}

type initiativeMemberDraft struct {
	taskRef       string
	operationID   string
	subjectDigest string
	task          domain.Task
}

// PrepareInitiative validates the complete graph before side effects, records
// every stable member intent, prepares reversible artifacts, and commits all
// durable records together. It never starts a task.
func (mutations *InitiativeMutations) PrepareInitiative(
	ctx context.Context,
	command PrepareInitiativeCommand,
) (InitiativePreparationResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return InitiativePreparationResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil {
		return InitiativePreparationResult{}, mutationValidationFailure("operation ID is invalid")
	}
	if domain.ValidateAuthorityReference("serviceInstanceId", command.ServiceInstanceID) != nil {
		return InitiativePreparationResult{}, mutationValidationFailure("service instance identity is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return InitiativePreparationResult{}, mutationValidationFailure("initiative subject cannot be encoded")
	}
	if replay, found, err := mutations.store.ReplayInitiativePreparation(
		ctx, command.OperationID, subjectDigest,
	); err != nil {
		return InitiativePreparationResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}

	now := mutations.clock()
	initiative, drafts, err := mutations.buildInitiative(command, now)
	if err != nil {
		return InitiativePreparationResult{}, mutationValidationFailure("initiative graph or member contract is invalid")
	}
	if err := mutations.validateInitiativeDependencies(ctx, command, drafts); err != nil {
		return InitiativePreparationResult{}, err
	}
	intentAt, err := mutations.recordMemberIntents(ctx, drafts, subjectDigest, now)
	if err != nil {
		return InitiativePreparationResult{}, err
	}
	initiative.CreatedAt = intentAt
	initiative.UpdatedAt = intentAt
	for index := range drafts {
		drafts[index].task.CreatedAt = intentAt
		drafts[index].task.UpdatedAt = intentAt
	}
	groupNonce, err := mutations.nonces()
	if err != nil {
		return InitiativePreparationResult{}, &dependencyFailure{message: "group registration identity source failed", cause: err}
	}
	if !registrationNoncePattern.MatchString(groupNonce) {
		return InitiativePreparationResult{}, mutationValidationFailure("group registration identity is invalid")
	}
	members, err := mutations.prepareInitiativeMembers(ctx, drafts, intentAt)
	if err != nil {
		return InitiativePreparationResult{}, err
	}
	return mutations.store.CommitPreparedInitiative(ctx, PreparedInitiativeMutation{
		Initiative: initiative, Members: members,
		GroupRegistrationNonce: groupNonce, GroupExpiresAt: intentAt.Add(mutations.preparationTTL).UTC(),
		OperationID: command.OperationID, SubjectDigest: subjectDigest, At: intentAt,
	})
}

func (mutations *InitiativeMutations) buildInitiative(
	command PrepareInitiativeCommand,
	at time.Time,
) (domain.DevelopmentInitiative, []initiativeMemberDraft, error) {
	memberCount := 0
	for _, component := range command.Components {
		memberCount += len(component.Tasks)
	}
	if memberCount == 0 || memberCount > maximumInitiativeMembers {
		return domain.DevelopmentInitiative{}, nil, errors.New("initiative member count is invalid")
	}
	initiative := domain.DevelopmentInitiative{
		SchemaVersion: 1,
		Handle:        initiativeIdentity(command.ServiceInstanceID, command.OperationID),
		TitleRef:      command.TitleRef, State: domain.InitiativePreparing,
		BaseRevisionSet:     append([]domain.InitiativeBaseRevision(nil), command.BaseRevisionSet...),
		ContractArtifacts:   append([]string(nil), command.ContractArtifacts...),
		IntegrationPolicyID: command.IntegrationPolicyID,
		StateVersion:        1, CreatedAt: at, UpdatedAt: at,
	}
	baseRevisions := make(map[string]string, len(command.BaseRevisionSet))
	for _, base := range command.BaseRevisionSet {
		baseRevisions[base.RepositoryID] = base.Revision
	}
	refs := make(map[string]string, memberCount)
	drafts := make([]initiativeMemberDraft, 0, memberCount)
	for _, component := range command.Components {
		domainComponent := domain.InitiativeComponent{
			ComponentHandle: component.ComponentHandle, RepositoryID: component.RepositoryID,
			ResponsibilityRef: component.ResponsibilityRef,
		}
		for _, member := range component.Tasks {
			if domain.ValidateTaskHandle(member.TaskRef) != nil || refs[member.TaskRef] != "" {
				return domain.DevelopmentInitiative{}, nil, errors.New("initiative task reference is invalid")
			}
			operationID := initiativeMemberOperationID(command.OperationID, member.TaskRef)
			taskHandle, err := mutations.taskIDs(operationID)
			if err != nil {
				return domain.DevelopmentInitiative{}, nil, err
			}
			refs[member.TaskRef] = taskHandle
			task := domain.Task{
				SchemaVersion: 1, Handle: taskHandle, ServiceInstanceID: command.ServiceInstanceID,
				State: domain.TaskPrepared, Shape: member.Contract.Shape,
				RepositoryID: component.RepositoryID, BaseRevision: baseRevisions[component.RepositoryID],
				BriefRevision:      1,
				AcceptanceCriteria: append([]string(nil), member.Contract.AcceptanceCriteria...),
				Constraints:        append([]string(nil), member.Contract.Constraints...),
				ConsumedContracts:  append([]domain.PinnedContract(nil), member.Contract.ConsumedContracts...),
				ValidationProfile:  member.Contract.ValidationProfile,
				DeliveryMode:       member.Contract.DeliveryMode, WorkerProfileID: member.Contract.WorkerProfileID,
				StateVersion: 1, CreatedAt: at, UpdatedAt: at,
			}
			task, err = task.PinBriefRevision()
			if err != nil {
				return domain.DevelopmentInitiative{}, nil, err
			}
			domainComponent.TaskHandles = append(domainComponent.TaskHandles, taskHandle)
			drafts = append(drafts, initiativeMemberDraft{taskRef: member.TaskRef, operationID: operationID, task: task})
		}
		initiative.Components = append(initiative.Components, domainComponent)
	}
	for _, edge := range command.Edges {
		from, fromFound := refs[edge.FromTaskRef]
		to, toFound := refs[edge.ToTaskRef]
		if !fromFound || !toFound {
			return domain.DevelopmentInitiative{}, nil, errors.New("initiative edge names a missing task")
		}
		initiative.Edges = append(initiative.Edges, domain.InitiativeEdge{
			FromTaskHandle: from, ToTaskHandle: to, Kind: edge.Kind,
			RequiredArtifactKind: edge.RequiredArtifactKind,
		})
	}
	if command.IntegrationOwnerTask != "" {
		owner, found := refs[command.IntegrationOwnerTask]
		if !found {
			return domain.DevelopmentInitiative{}, nil, errors.New("initiative owner names a missing task")
		}
		initiative.IntegrationOwnerTask = owner
	}
	if err := initiative.Validate(); err != nil {
		return domain.DevelopmentInitiative{}, nil, err
	}
	return initiative, drafts, nil
}

func (mutations *InitiativeMutations) validateInitiativeDependencies(
	ctx context.Context,
	command PrepareInitiativeCommand,
	drafts []initiativeMemberDraft,
) error {
	repositories := make(map[string]struct{})
	for _, base := range command.BaseRevisionSet {
		repositories[base.RepositoryID] = struct{}{}
	}
	repositoryIDs := make([]string, 0, len(repositories))
	for repositoryID := range repositories {
		repositoryIDs = append(repositoryIDs, repositoryID)
	}
	sort.Strings(repositoryIDs)
	for _, repositoryID := range repositoryIDs {
		if err := mutations.repositories.ValidateRepository(ctx, repositoryID); err != nil {
			return &dependencyFailure{message: "initiative repository validation failed", cause: err}
		}
	}
	for _, draft := range drafts {
		if err := mutations.workerProfiles(draft.task.WorkerProfileID, draft.task.Shape); err != nil {
			return mutationValidationFailure("initiative worker profile is unavailable")
		}
		if err := mutations.validationProfiles(draft.task.ValidationProfile, draft.task.Shape); err != nil {
			return mutationValidationFailure("initiative validation profile is unavailable")
		}
	}
	return nil
}

func (mutations *InitiativeMutations) recordMemberIntents(
	ctx context.Context,
	drafts []initiativeMemberDraft,
	subjectDigest string,
	at time.Time,
) (time.Time, error) {
	intentAt := time.Time{}
	for index := range drafts {
		draft := drafts[index]
		memberDigest := fmt.Sprintf("%x", sha256.Sum256([]byte(subjectDigest+"\x00"+draft.taskRef)))
		intent, err := mutations.store.RecordTaskPreparationIntent(ctx, TaskPreparationIntent{
			OperationID: draft.operationID, TaskHandle: draft.task.Handle,
			SubjectDigest: memberDigest, CreatedAt: at,
		})
		if err != nil {
			return time.Time{}, &dependencyFailure{message: "initiative member preparation intent failed", cause: err}
		}
		if intent.Validate() != nil || intent.TaskHandle != draft.task.Handle ||
			intent.OperationID != draft.operationID || intent.SubjectDigest != memberDigest {
			return time.Time{}, &dependencyFailure{message: "initiative member preparation intent differs"}
		}
		if intentAt.IsZero() {
			intentAt = intent.CreatedAt
		} else if !intent.CreatedAt.Equal(intentAt) {
			return time.Time{}, &dependencyFailure{message: "initiative member preparation times differ"}
		}
		drafts[index].subjectDigest = memberDigest
	}
	return intentAt, nil
}

func (mutations *InitiativeMutations) prepareInitiativeMembers(
	ctx context.Context,
	drafts []initiativeMemberDraft,
	at time.Time,
) ([]PreparedInitiativeMember, error) {
	members := make([]PreparedInitiativeMember, 0, len(drafts))
	for _, draft := range drafts {
		workspace, err := mutations.workspaces.PrepareWorkspace(ctx, WorkspacePreparationRequest{
			OperationID: draft.operationID, TaskHandle: draft.task.Handle,
			RepositoryID: draft.task.RepositoryID, BaseRevision: draft.task.BaseRevision,
		})
		if err != nil {
			return nil, &dependencyFailure{message: "initiative workspace preparation failed", cause: err}
		}
		if workspace.CanonicalRoot != "" &&
			(!filepath.IsAbs(workspace.CanonicalRoot) || filepath.Clean(workspace.CanonicalRoot) != workspace.CanonicalRoot) {
			return nil, &dependencyFailure{message: "initiative workspace preparation returned an invalid root"}
		}
		brief, err := draft.task.RenderWorkerBrief()
		if err != nil {
			return nil, mutationValidationFailure("initiative member brief is invalid")
		}
		attachment, err := mutations.attachments.PrepareRuntimeAttachment(ctx, RuntimeAttachmentPreparationRequest{
			OperationID: draft.operationID, TaskHandle: draft.task.Handle,
			BriefRevision: draft.task.BriefRevision, BriefRevisionHash: draft.task.BriefRevisionHash,
			Brief: brief, WorkingDirectory: workspace.CanonicalRoot,
		})
		if err != nil {
			return nil, &dependencyFailure{message: "initiative runtime attachment preparation failed", cause: err}
		}
		if attachment.Validate() != nil {
			return nil, &dependencyFailure{message: "initiative runtime attachment source is invalid"}
		}
		nonce, err := mutations.nonces()
		if err != nil {
			return nil, &dependencyFailure{message: "initiative member registration identity source failed", cause: err}
		}
		preparation := ManagedRunPreparation{
			ExternalRunRef: draft.task.Handle, RegistrationNonce: nonce,
			RequestedWorkspaceRoot: workspace.CanonicalRoot, RequestedAttachment: attachment,
			ExpiresAt: at.Add(mutations.preparationTTL).UTC(), State: PreparationOpen,
		}
		if preparation.Validate(at) != nil {
			return nil, mutationValidationFailure("initiative member managed-run preparation is invalid")
		}
		members = append(members, PreparedInitiativeMember{
			Task: draft.task, Preparation: preparation,
			OperationID: draft.operationID, SubjectDigest: draft.subjectDigest,
		})
	}
	return members, nil
}

func initiativeIdentity(serviceInstanceID, operationID string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(serviceInstanceID+"\x00"+operationID)))
	return "initiative-" + digest[:24]
}

func initiativeMemberOperationID(operationID, taskRef string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(operationID+"\x00"+taskRef)))
	return "prepare-member-" + digest[:32]
}
