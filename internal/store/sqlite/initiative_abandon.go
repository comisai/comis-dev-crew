package sqlite

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandAbandonManagedRunGroup = "AbandonManagedRunGroup"

const initiativeAbandonmentMigration = `
CREATE TABLE initiative_group_abandon_members (
    operation_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    managed_run_id TEXT NOT NULL,
    external_run_ref TEXT NOT NULL,
    outcome TEXT NOT NULL,
    PRIMARY KEY(operation_id, managed_run_id),
    UNIQUE(operation_id, ordinal),
    FOREIGN KEY(operation_id) REFERENCES operations(id)
);
INSERT INTO schema_migrations(version, applied_at)
VALUES (36, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

var _ application.InitiativeAbandonmentStore = (*Store)(nil)

// ReplayInitiativeAbandonment returns the original per-member closure result.
func (store *Store) ReplayInitiativeAbandonment(
	ctx context.Context,
	operationID string,
	subjectDigest string,
) (application.InitiativeAbandonmentResult, bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, false, fmt.Errorf("begin initiative abandonment replay: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(
		ctx, transaction, operationID, commandAbandonManagedRunGroup, subjectDigest,
	)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, false, commitReplayConflict(transaction, err)
	}
	if !found {
		return application.InitiativeAbandonmentResult{}, false, nil
	}
	result, err := initiativeAbandonmentResult(ctx, transaction, operation)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativeAbandonmentResult{}, false, fmt.Errorf("commit initiative abandonment replay: %w", err)
	}
	return result, true, nil
}

// CommitInitiativeAbandonment closes every exact member in one transaction.
func (store *Store) CommitInitiativeAbandonment(
	ctx context.Context,
	mutation application.ManagedRunGroupAbandonmentMutation,
) (application.InitiativeAbandonmentResult, error) {
	if err := validateManagedRunGroupAbandonmentMutation(mutation); err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, fmt.Errorf("begin initiative abandonment: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if operation, found, err := mutationReplay(
		ctx, transaction, mutation.OperationID, commandAbandonManagedRunGroup, mutation.SubjectDigest,
	); err != nil {
		return application.InitiativeAbandonmentResult{}, commitReplayConflict(transaction, err)
	} else if found {
		result, err := initiativeAbandonmentResult(ctx, transaction, operation)
		if err != nil {
			return application.InitiativeAbandonmentResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return application.InitiativeAbandonmentResult{}, fmt.Errorf("commit initiative abandonment replay: %w", err)
		}
		return result, nil
	}

	initiativeHandle, _, err := initiativePreparationByNonce(ctx, transaction, mutation.RegistrationNonce)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	initiative, err := getInitiative(ctx, transaction, initiativeHandle)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	if (initiative.State != domain.InitiativePreparing && initiative.State != domain.InitiativeUnknown) ||
		(initiative.ManagedRunGroupID != "" && initiative.ManagedRunGroupID != mutation.ManagedRunGroupID) {
		return application.InitiativeAbandonmentResult{}, fmt.Errorf("initiative abandonment posture: %w", application.ErrPrecondition)
	}
	handles := initiativeTaskHandles(initiative)
	if len(handles) != len(mutation.Members) {
		return application.InitiativeAbandonmentResult{}, fmt.Errorf("initiative abandonment member set: %w", application.ErrPrecondition)
	}
	initiativeMembers := make(map[string]struct{}, len(handles))
	for _, handle := range handles {
		initiativeMembers[handle] = struct{}{}
	}

	tasks := make([]domain.Task, 0, len(mutation.Members))
	outcomes := make([]application.InitiativeActivationMemberResult, 0, len(mutation.Members))
	partial := false
	for _, member := range mutation.Members {
		task, taskErr := getTask(ctx, transaction, member.ExternalRunRef)
		if taskErr != nil {
			return application.InitiativeAbandonmentResult{}, taskErr
		}
		if _, belongs := initiativeMembers[task.Handle]; !belongs {
			return application.InitiativeAbandonmentResult{}, fmt.Errorf("initiative abandonment member set: %w", application.ErrPrecondition)
		}
		preparation, preparationErr := getManagedRunPreparation(ctx, transaction, task)
		if preparationErr != nil {
			return application.InitiativeAbandonmentResult{}, preparationErr
		}
		if task.ServiceInstanceID != mutation.ServiceInstanceID || preparation.State != application.PreparationOpen ||
			preparation.ExternalRunRef != member.ExternalRunRef || subtle.ConstantTimeCompare(
			[]byte(preparation.RegistrationNonce), []byte(member.RegistrationNonce),
		) != 1 || (task.ManagedRunID != "" && task.ManagedRunID != member.ManagedRunID) {
			return application.InitiativeAbandonmentResult{}, fmt.Errorf("initiative abandonment member join: %w", application.ErrPrecondition)
		}
		if mutation.At.Before(task.UpdatedAt) {
			return application.InitiativeAbandonmentResult{}, fmt.Errorf("initiative abandonment time: %w", application.ErrPrecondition)
		}
		updated, outcome, transitionErr := abandonInitiativeMember(task, mutation.Disposition, mutation.At)
		if transitionErr != nil {
			return application.InitiativeAbandonmentResult{}, transitionErr
		}
		if outcome == application.InitiativeActivationUnknown {
			partial = true
		}
		closedAt := mutation.At
		preparation.State = application.PreparationAbandoned
		preparation.AbandonReason = mutation.Reason
		preparation.Disposition = mutation.Disposition
		preparation.ClosedAt = &closedAt
		if err := updateManagedRunPreparation(ctx, transaction, updated, preparation); err != nil {
			return application.InitiativeAbandonmentResult{}, err
		}
		tasks = append(tasks, updated)
		outcomes = append(outcomes, application.InitiativeActivationMemberResult{
			ManagedRunID: member.ManagedRunID, Outcome: outcome,
		})
	}

	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	for index := range tasks {
		tasks[index].StateVersion = stateVersion
		tasks[index].UpdatedAt = mutation.At
		if err := tasks[index].Validate(); err != nil {
			return application.InitiativeAbandonmentResult{}, err
		}
		if err := updateInitiativeMemberTask(ctx, transaction, tasks[index]); err != nil {
			return application.InitiativeAbandonmentResult{}, err
		}
	}
	initiative.ManagedRunGroupID = mutation.ManagedRunGroupID
	initiative.State = domain.InitiativeCancelled
	if partial || mutation.Disposition == application.AbandonDispositionPreserve {
		initiative.State = domain.InitiativeUnknown
	}
	initiative.StateVersion = stateVersion
	initiative.UpdatedAt = mutation.At
	if err := initiative.Validate(); err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	if err := updateInitiativeRecord(ctx, transaction, initiative); err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandAbandonManagedRunGroup, mutation.SubjectDigest,
		initiative.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		if isConstraintError(err) {
			return application.InitiativeAbandonmentResult{}, fmt.Errorf("insert initiative abandonment operation: %w", application.ErrConflict)
		}
		return application.InitiativeAbandonmentResult{}, err
	}
	for index, member := range mutation.Members {
		if err := insertInitiativeAbandonmentMember(ctx, transaction, mutation.OperationID, index, member, outcomes[index]); err != nil {
			return application.InitiativeAbandonmentResult{}, err
		}
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativeAbandonmentResult{}, fmt.Errorf("commit initiative abandonment: %w", err)
	}
	return application.InitiativeAbandonmentResult{
		Initiative: initiative, Operation: operation, Members: outcomes, Disposition: mutation.Disposition,
	}, nil
}

func abandonInitiativeMember(
	task domain.Task,
	disposition application.AbandonDisposition,
	at time.Time,
) (domain.Task, application.InitiativeActivationOutcome, error) {
	switch task.State {
	case domain.TaskPrepared:
		transition := domain.TransitionPreparationPreserved
		if disposition == application.AbandonDispositionReapSafe {
			transition = domain.TransitionPreparationAbandoned
		}
		updated, err := task.ApplyTransition(transition, at)
		return updated, application.InitiativeActivationCompleted, err
	case domain.TaskReady:
		updated, err := task.ApplyTransition(domain.TransitionCancelRequested, at)
		return updated, application.InitiativeActivationCompleted, err
	case domain.TaskUnknown:
		return task, application.InitiativeActivationUnknown, nil
	default:
		return domain.Task{}, "", fmt.Errorf("initiative member is no longer safe to abandon: %w", application.ErrPrecondition)
	}
}

func validateManagedRunGroupAbandonmentMutation(mutation application.ManagedRunGroupAbandonmentMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		domain.ValidateAuthorityReference("serviceInstanceId", mutation.ServiceInstanceID) != nil ||
		domain.ValidateAuthorityReference("managedRunGroupId", mutation.ManagedRunGroupID) != nil ||
		mutation.RegistrationNonce == "" || mutation.At.Location() != time.UTC ||
		(mutation.Reason != application.AbandonReasonActivationRejected &&
			mutation.Reason != application.AbandonReasonOwnerCancelled &&
			mutation.Reason != application.AbandonReasonRegistrationExpired &&
			mutation.Reason != application.AbandonReasonServiceUnavailable) ||
		(mutation.Disposition != application.AbandonDispositionPreserve &&
			mutation.Disposition != application.AbandonDispositionReapSafe) ||
		len(mutation.Members) == 0 || len(mutation.Members) > 16 {
		return application.ErrInvalidInput
	}
	externalRefs := make(map[string]struct{}, len(mutation.Members))
	managedRuns := make(map[string]struct{}, len(mutation.Members))
	for _, member := range mutation.Members {
		if domain.ValidateAuthorityReference("managedRunId", member.ManagedRunID) != nil ||
			domain.ValidateTaskHandle(member.ExternalRunRef) != nil || member.RegistrationNonce == "" {
			return application.ErrInvalidInput
		}
		if _, duplicate := externalRefs[member.ExternalRunRef]; duplicate {
			return application.ErrInvalidInput
		}
		if _, duplicate := managedRuns[member.ManagedRunID]; duplicate {
			return application.ErrInvalidInput
		}
		externalRefs[member.ExternalRunRef] = struct{}{}
		managedRuns[member.ManagedRunID] = struct{}{}
	}
	return nil
}

func insertInitiativeAbandonmentMember(
	ctx context.Context,
	target execer,
	operationID string,
	ordinal int,
	member application.ManagedRunGroupAbandonmentMember,
	result application.InitiativeActivationMemberResult,
) error {
	const statement = `INSERT INTO initiative_group_abandon_members (
		operation_id, ordinal, managed_run_id, external_run_ref, outcome
	) VALUES (?, ?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement, operationID, ordinal, member.ManagedRunID, member.ExternalRunRef, result.Outcome)
	if err != nil {
		return fmt.Errorf("insert initiative abandonment member: %w", err)
	}
	return nil
}

func initiativeAbandonmentResult(
	ctx context.Context,
	source queryer,
	operation domain.OperationRecord,
) (result application.InitiativeAbandonmentResult, resultErr error) {
	initiative, err := getInitiative(ctx, source, operation.ResultRef)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	const query = `SELECT members.managed_run_id, members.outcome, preparations.disposition
		FROM initiative_group_abandon_members AS members
		JOIN task_preparations AS preparations ON preparations.task_handle = members.external_run_ref
		WHERE members.operation_id = ? ORDER BY members.ordinal`
	rows, err := source.QueryContext(ctx, query, operation.ID)
	if err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	members := make([]application.InitiativeActivationMemberResult, 0)
	disposition := application.AbandonDisposition("")
	for rows.Next() {
		var managedRunID string
		var outcome application.InitiativeActivationOutcome
		var memberDisposition application.AbandonDisposition
		if err := rows.Scan(&managedRunID, &outcome, &memberDisposition); err != nil {
			return application.InitiativeAbandonmentResult{}, err
		}
		if outcome != application.InitiativeActivationCompleted && outcome != application.InitiativeActivationUnknown {
			return application.InitiativeAbandonmentResult{}, errors.New("stored initiative abandonment outcome is invalid")
		}
		if disposition == "" {
			disposition = memberDisposition
		} else if disposition != memberDisposition {
			return application.InitiativeAbandonmentResult{}, errors.New("stored initiative abandonment disposition differs")
		}
		members = append(members, application.InitiativeActivationMemberResult{
			ManagedRunID: managedRunID, Outcome: outcome,
		})
	}
	if err := rows.Err(); err != nil {
		return application.InitiativeAbandonmentResult{}, err
	}
	if len(members) == 0 || (disposition != application.AbandonDispositionPreserve && disposition != application.AbandonDispositionReapSafe) {
		return application.InitiativeAbandonmentResult{}, errors.New("stored initiative abandonment result is incomplete")
	}
	return application.InitiativeAbandonmentResult{
		Initiative: initiative, Operation: operation, Members: members, Disposition: disposition,
	}, nil
}
