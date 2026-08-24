package sqlite

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandActivateManagedRunGroup = "ActivateManagedRunGroup"

var _ application.InitiativeActivationStore = (*Store)(nil)

// ReplayInitiativeActivation returns the exact committed group join.
func (store *Store) ReplayInitiativeActivation(
	ctx context.Context,
	operationID string,
	subjectDigest string,
) (application.InitiativeActivationResult, bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativeActivationResult{}, false, fmt.Errorf("begin initiative activation replay: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(
		ctx, transaction, operationID, commandActivateManagedRunGroup, subjectDigest,
	)
	if err != nil {
		return application.InitiativeActivationResult{}, false, commitReplayConflict(transaction, err)
	}
	if !found {
		return application.InitiativeActivationResult{}, false, nil
	}
	result, err := initiativeActivationResult(ctx, transaction, operation)
	if err != nil {
		return application.InitiativeActivationResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativeActivationResult{}, false, fmt.Errorf("commit initiative activation replay: %w", err)
	}
	return result, true, nil
}

// CommitInitiativeActivation validates and binds the complete member set in one transaction.
func (store *Store) CommitInitiativeActivation(
	ctx context.Context,
	mutation application.ManagedRunGroupActivationMutation,
) (application.InitiativeActivationResult, error) {
	if err := validateManagedRunGroupActivationMutation(mutation); err != nil {
		return application.InitiativeActivationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativeActivationResult{}, fmt.Errorf("begin initiative activation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if operation, found, err := mutationReplay(
		ctx, transaction, mutation.OperationID, commandActivateManagedRunGroup, mutation.SubjectDigest,
	); err != nil {
		return application.InitiativeActivationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		result, err := initiativeActivationResult(ctx, transaction, operation)
		if err != nil {
			return application.InitiativeActivationResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return application.InitiativeActivationResult{}, fmt.Errorf("commit initiative activation replay: %w", err)
		}
		return result, nil
	}

	initiativeHandle, expiresAt, err := initiativePreparationByNonce(ctx, transaction, mutation.RegistrationNonce)
	if err != nil {
		return application.InitiativeActivationResult{}, err
	}
	initiative, err := getInitiative(ctx, transaction, initiativeHandle)
	if err != nil {
		return application.InitiativeActivationResult{}, err
	}
	if (initiative.State != domain.InitiativePreparing && initiative.State != domain.InitiativeUnknown) ||
		initiative.ManagedRunGroupID != "" || mutation.At.Location() != time.UTC || !mutation.At.Before(expiresAt) {
		return application.InitiativeActivationResult{}, fmt.Errorf("initiative activation posture: %w", application.ErrPrecondition)
	}
	members := make(map[string]application.ManagedRunGroupActivationMember, len(mutation.Members))
	for _, member := range mutation.Members {
		members[member.ExternalRunRef] = member
	}
	handles := initiativeTaskHandles(initiative)
	if len(handles) != len(members) {
		return application.InitiativeActivationResult{}, fmt.Errorf("initiative activation member set: %w", application.ErrPrecondition)
	}
	boundTasks := make([]domain.Task, 0, len(handles))
	for _, handle := range handles {
		member, exists := members[handle]
		if !exists {
			return application.InitiativeActivationResult{}, fmt.Errorf("initiative activation member set: %w", application.ErrPrecondition)
		}
		task, err := getTask(ctx, transaction, handle)
		if err != nil {
			return application.InitiativeActivationResult{}, err
		}
		preparation, err := getManagedRunPreparation(ctx, transaction, task)
		if err != nil {
			return application.InitiativeActivationResult{}, fmt.Errorf("read initiative member preparation: %w", err)
		}
		if task.ServiceInstanceID != mutation.ServiceInstanceID || preparation.State != application.PreparationOpen ||
			preparation.ExternalRunRef != handle || subtle.ConstantTimeCompare(
			[]byte(preparation.RegistrationNonce), []byte(member.RegistrationNonce),
		) != 1 || preparation.RequestedWorkspaceRoot == "" || preparation.RequestedAttachment.Validate() != nil ||
			task.State != domain.TaskPrepared || task.ManagedRunID != "" || task.WorkspaceLeaseID != "" ||
			task.ExecutionAttachmentID != "" || task.AttachmentTargetName != "" {
			return application.InitiativeActivationResult{}, fmt.Errorf("initiative member activation join: %w", application.ErrPrecondition)
		}
		bound, err := task.AcknowledgeBinding(member.Binding, mutation.At)
		if err != nil {
			return application.InitiativeActivationResult{}, fmt.Errorf("apply initiative member binding: %w", err)
		}
		bound.ExecutionAttachmentID = member.ExecutionAttachmentID
		bound.AttachmentTargetName = member.AttachmentTargetName
		boundTasks = append(boundTasks, bound)
	}

	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.InitiativeActivationResult{}, err
	}
	for index := range boundTasks {
		boundTasks[index].StateVersion = stateVersion
		if err := boundTasks[index].Validate(); err != nil {
			return application.InitiativeActivationResult{}, fmt.Errorf("validate initiative member binding: %w", err)
		}
		if err := updateInitiativeMemberTask(ctx, transaction, boundTasks[index]); err != nil {
			return application.InitiativeActivationResult{}, err
		}
	}
	initiative.ManagedRunGroupID = mutation.ManagedRunGroupID
	initiative.State = domain.InitiativeUnknown
	initiative.StateVersion = stateVersion
	initiative.UpdatedAt = mutation.At
	if err := initiative.Validate(); err != nil {
		return application.InitiativeActivationResult{}, fmt.Errorf("validate bound initiative: %w", err)
	}
	if err := updateInitiativeRecord(ctx, transaction, initiative); err != nil {
		return application.InitiativeActivationResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandActivateManagedRunGroup, mutation.SubjectDigest,
		initiative.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.InitiativeActivationResult{}, fmt.Errorf("insert initiative activation operation: %w", application.ErrConflict)
		}
		return application.InitiativeActivationResult{}, fmt.Errorf("insert initiative activation operation: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativeActivationResult{}, fmt.Errorf("commit initiative activation: %w", err)
	}
	sort.Slice(boundTasks, func(left, right int) bool { return boundTasks[left].Handle < boundTasks[right].Handle })
	return application.InitiativeActivationResult{
		Initiative: initiative, Tasks: boundTasks, Operation: operation,
	}, nil
}

// SetInitiativeActivationState changes only the post-bind launchability posture.
func (store *Store) SetInitiativeActivationState(
	ctx context.Context,
	managedRunGroupID string,
	state domain.InitiativeState,
	at time.Time,
) (domain.DevelopmentInitiative, error) {
	if domain.ValidateAuthorityReference("managedRunGroupId", managedRunGroupID) != nil ||
		(state != domain.InitiativeActive && state != domain.InitiativeUnknown) || at.Location() != time.UTC {
		return domain.DevelopmentInitiative{}, application.ErrInvalidInput
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("begin initiative activation posture: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	initiative, err := getInitiativeByManagedRunGroup(ctx, transaction, managedRunGroupID)
	if err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	if initiative.State == state {
		if err := transaction.Commit(); err != nil {
			return domain.DevelopmentInitiative{}, fmt.Errorf("commit initiative activation posture replay: %w", err)
		}
		return initiative, nil
	}
	if initiative.State != domain.InitiativeUnknown || at.Before(initiative.UpdatedAt) {
		return domain.DevelopmentInitiative{}, application.ErrPrecondition
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	initiative.State = state
	initiative.StateVersion = stateVersion
	initiative.UpdatedAt = at
	if err := initiative.Validate(); err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	if err := updateInitiativeRecord(ctx, transaction, initiative); err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	if err := transaction.Commit(); err != nil {
		return domain.DevelopmentInitiative{}, fmt.Errorf("commit initiative activation posture: %w", err)
	}
	return initiative, nil
}

func validateManagedRunGroupActivationMutation(mutation application.ManagedRunGroupActivationMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		domain.ValidateAuthorityReference("serviceInstanceId", mutation.ServiceInstanceID) != nil ||
		domain.ValidateAuthorityReference("managedRunGroupId", mutation.ManagedRunGroupID) != nil ||
		mutation.RegistrationNonce == "" || mutation.At.Location() != time.UTC ||
		len(mutation.Members) == 0 || len(mutation.Members) > domain.MaximumInitiativeMembers {
		return application.ErrInvalidInput
	}
	externalRefs := make(map[string]struct{}, len(mutation.Members))
	managedRuns := make(map[string]struct{}, len(mutation.Members))
	for _, member := range mutation.Members {
		if domain.ValidateTaskHandle(member.ExternalRunRef) != nil || member.Binding.Validate() != nil ||
			member.RegistrationNonce == "" ||
			domain.ValidateAuthorityReference("executionAttachmentId", member.ExecutionAttachmentID) != nil ||
			domain.ValidateAttachmentTargetName(member.AttachmentTargetName) != nil {
			return application.ErrInvalidInput
		}
		if _, exists := externalRefs[member.ExternalRunRef]; exists {
			return application.ErrInvalidInput
		}
		if _, exists := managedRuns[member.Binding.ManagedRunID]; exists {
			return application.ErrInvalidInput
		}
		externalRefs[member.ExternalRunRef] = struct{}{}
		managedRuns[member.Binding.ManagedRunID] = struct{}{}
	}
	return nil
}

func initiativePreparationByNonce(
	ctx context.Context,
	source queryer,
	registrationNonce string,
) (string, time.Time, error) {
	const query = `SELECT initiative_handle, registration_nonce, expires_at
		FROM initiative_preparations WHERE registration_nonce = ?`
	var handle, storedNonce, expiresAtText string
	err := source.QueryRowContext(ctx, query, registrationNonce).Scan(&handle, &storedNonce, &expiresAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, fmt.Errorf("get initiative activation preparation: %w", application.ErrNotFound)
	}
	if err != nil {
		return "", time.Time{}, fmt.Errorf("get initiative activation preparation: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(storedNonce), []byte(registrationNonce)) != 1 {
		return "", time.Time{}, fmt.Errorf("initiative activation registration identity: %w", application.ErrPrecondition)
	}
	expiresAt, err := parseTime(expiresAtText)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("parse initiative activation expiry: %w", err)
	}
	return handle, expiresAt, nil
}

func initiativeActivationResult(
	ctx context.Context,
	source queryer,
	operation domain.OperationRecord,
) (application.InitiativeActivationResult, error) {
	initiative, err := getInitiative(ctx, source, operation.ResultRef)
	if err != nil {
		return application.InitiativeActivationResult{}, err
	}
	handles := initiativeTaskHandles(initiative)
	tasks := make([]domain.Task, 0, len(handles))
	for _, handle := range handles {
		task, err := getTask(ctx, source, handle)
		if err != nil {
			return application.InitiativeActivationResult{}, err
		}
		tasks = append(tasks, task)
	}
	return application.InitiativeActivationResult{
		Initiative: initiative, Tasks: tasks, Operation: operation,
	}, nil
}

func getInitiativeByManagedRunGroup(
	ctx context.Context,
	source queryer,
	managedRunGroupID string,
) (domain.DevelopmentInitiative, error) {
	const query = `SELECT handle FROM initiatives WHERE managed_run_group_id = ?`
	var handle string
	err := source.QueryRowContext(ctx, query, managedRunGroupID).Scan(&handle)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.DevelopmentInitiative{}, application.ErrNotFound
	}
	if err != nil {
		return domain.DevelopmentInitiative{}, err
	}
	return getInitiative(ctx, source, handle)
}

func updateInitiativeMemberTask(ctx context.Context, target execer, task domain.Task) error {
	const update = `UPDATE tasks SET
		managed_run_id = ?, workspace_lease_id = ?, execution_attachment_id = ?, attachment_target_name = ?,
		state = ?, state_version = ?, updated_at = ?
		WHERE handle = ?`
	result, err := target.ExecContext(ctx, update,
		task.ManagedRunID, task.WorkspaceLeaseID, task.ExecutionAttachmentID, task.AttachmentTargetName,
		task.State, task.StateVersion, formatTime(task.UpdatedAt), task.Handle,
	)
	if err != nil {
		return fmt.Errorf("update initiative member task: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update initiative member task: exact task was not updated")
	}
	return nil
}

func updateInitiativeRecord(
	ctx context.Context,
	target queryExecer,
	initiative domain.DevelopmentInitiative,
) error {
	var previous domain.InitiativeState
	if err := target.QueryRowContext(ctx,
		`SELECT state FROM initiatives WHERE handle = ?`, initiative.Handle,
	).Scan(&previous); err != nil {
		return fmt.Errorf("read initiative state before update: %w", err)
	}
	if err := refuseReservedIntegrationInitiativeTransition(
		ctx, target, initiative.Handle, previous, initiative.State,
	); err != nil {
		return err
	}
	const update = `UPDATE initiatives SET
		managed_run_group_id = ?, state = ?, state_version = ?, updated_at = ?
		WHERE handle = ?`
	result, err := target.ExecContext(ctx, update,
		initiative.ManagedRunGroupID, initiative.State, initiative.StateVersion,
		formatTime(initiative.UpdatedAt), initiative.Handle,
	)
	if err != nil {
		return fmt.Errorf("update initiative record: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update initiative record: exact initiative was not updated")
	}
	return refreshInitiativeLaunchFacts(ctx, target, initiative)
}
