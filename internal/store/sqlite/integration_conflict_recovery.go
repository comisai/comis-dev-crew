package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

func resolveIntegrationRecoveryReservation(
	ctx context.Context,
	transaction *sql.Tx,
	request application.IntegrationReservationRequest,
) (integrationApplicationRow, error) {
	previous, found, err := findIntegrationApplication(ctx, transaction, request.Command.RecoveryOperationID)
	if err != nil {
		return integrationApplicationRow{}, err
	}
	if !found || previous.status != string(application.IntegrationConflicted) ||
		previous.strategy != application.IntegrationRebase {
		return integrationApplicationRow{}, fmt.Errorf("integration recovery receipt is unavailable: %w", application.ErrPrecondition)
	}
	if !integrationRecoveryMatchesRequest(previous, request) || request.At.Before(previous.completedAt) {
		return integrationApplicationRow{}, fmt.Errorf("integration recovery identity differs: %w", application.ErrConflict)
	}
	if existing, found, err := findIntegrationRecoveryApplication(
		ctx, transaction, previous.operationID,
	); err != nil {
		return integrationApplicationRow{}, err
	} else if found && existing.operationID != request.Command.OperationID {
		return integrationApplicationRow{}, fmt.Errorf("integration conflict already has a recovery operation: %w", application.ErrIntegrationApplicationExists)
	}
	if err := validateCurrentIntegrationRecoveryAuthority(ctx, transaction, previous, request.At); err != nil {
		return integrationApplicationRow{}, err
	}
	recovery := previous
	recovery.operationID = request.Command.OperationID
	recovery.recoveryOperationID = previous.operationID
	recovery.subjectDigest = request.SubjectDigest
	recovery.status = "reserved"
	recovery.resultingHead = ""
	recovery.conflicts = []string{}
	recovery.reservedAt = request.At
	recovery.completedAt = time.Time{}
	recovery.stateVersion = 0
	return recovery, nil
}

func integrationRecoveryMatchesRequest(
	previous integrationApplicationRow,
	request application.IntegrationReservationRequest,
) bool {
	command := request.Command
	return previous.operationID == command.RecoveryOperationID &&
		previous.initiativeHandle == command.InitiativeHandle &&
		previous.integrationTaskHandle == command.IntegrationTaskHandle &&
		previous.candidateTaskHandle == command.CandidateTaskHandle &&
		previous.candidateHead == command.CandidateHead &&
		previous.expectedTargetHead == command.ExpectedIntegrationHead &&
		previous.policyID == request.PolicyID && previous.strategy == request.Strategy
}

func validateCurrentIntegrationRecoveryAuthority(
	ctx context.Context,
	source queryer,
	previous integrationApplicationRow,
	at time.Time,
) error {
	initiative, err := getInitiative(ctx, source, previous.initiativeHandle)
	if err != nil {
		return err
	}
	if (initiative.State != domain.InitiativeActive && initiative.State != domain.InitiativeIntegrating) ||
		initiative.IntegrationPolicyID != previous.policyID ||
		initiative.AuthorizeIntegrationWrite(previous.integrationTaskHandle) != nil {
		return fmt.Errorf("integration recovery authority is unavailable: %w", application.ErrPrecondition)
	}
	integrationTask, err := getTask(ctx, source, previous.integrationTaskHandle)
	if err != nil {
		return err
	}
	candidateTask, err := getTask(ctx, source, previous.candidateTaskHandle)
	if err != nil {
		return err
	}
	if !integrationRecoveryTasksMatch(initiative, integrationTask, candidateTask, previous) {
		return fmt.Errorf("integration recovery task authority differs: %w", application.ErrPrecondition)
	}
	if err := validateIntegrationRecoveryWorktrees(ctx, source, integrationTask, candidateTask, previous); err != nil {
		return err
	}
	writable, err := integrationOwnerWritableForRecovery(ctx, source, initiative, integrationTask)
	if err != nil {
		return err
	}
	if !writable {
		return fmt.Errorf("integration recovery owner is not writable: %w", application.ErrPrecondition)
	}
	evidence, err := latestCandidateEvidenceRow(ctx, source, candidateTask.Handle)
	if err != nil {
		return err
	}
	sealed, err := domain.ParseDeliveryEvidence(evidence.canonical, evidence.digest)
	if err != nil || evidence.judgment.Outcome != domain.CandidateAccepted ||
		evidence.digest != previous.evidenceDigest || sealed.Bundle().HeadRevision != previous.candidateHead {
		return fmt.Errorf("integration recovery evidence differs: %w", application.ErrPrecondition)
	}
	if at.IsZero() || at.Location() != time.UTC {
		return errors.New("integration recovery time is invalid")
	}
	return nil
}

func integrationRecoveryTasksMatch(
	initiative domain.DevelopmentInitiative,
	integrationTask domain.Task,
	candidateTask domain.Task,
	previous integrationApplicationRow,
) bool {
	repositoryID, candidateFound := initiativeRepositoryForTask(initiative, candidateTask.Handle)
	targetRepositoryID, targetFound := initiativeRepositoryForTask(initiative, integrationTask.Handle)
	return candidateFound && targetFound && repositoryID == previous.repositoryID && targetRepositoryID == repositoryID &&
		candidateTask.RepositoryID == repositoryID && integrationTask.RepositoryID == repositoryID &&
		candidateTask.BaseRevision == previous.candidateBase &&
		integrationTask.BaseRevision == initiativeBaseForRepository(initiative, repositoryID) &&
		(candidateTask.State == domain.TaskCandidateComplete || candidateTask.State == domain.TaskDelivered)
}

func validateIntegrationRecoveryWorktrees(
	ctx context.Context,
	source queryer,
	integrationTask domain.Task,
	candidateTask domain.Task,
	previous integrationApplicationRow,
) error {
	target, err := getManagedRunPreparation(ctx, source, integrationTask)
	if err != nil {
		return err
	}
	candidate, err := getManagedRunPreparation(ctx, source, candidateTask)
	if err != nil {
		return err
	}
	if target.State != application.PreparationOpen || candidate.State != application.PreparationOpen ||
		target.RequestedWorkspaceRoot != previous.targetWorktree ||
		candidate.RequestedWorkspaceRoot != previous.candidateWorktree {
		return fmt.Errorf("integration recovery worktree authority differs: %w", application.ErrPrecondition)
	}
	return nil
}

func integrationOwnerWritableForRecovery(
	ctx context.Context,
	source queryer,
	initiative domain.DevelopmentInitiative,
	integrationTask domain.Task,
) (bool, error) {
	if integrationTask.State == domain.TaskWorking || integrationTask.State == domain.TaskAwaitingDecision ||
		integrationTask.State == domain.TaskBlocked {
		return true, nil
	}
	if integrationTask.State != domain.TaskReady {
		return false, nil
	}
	deliverySatisfied := make(map[string]bool)
	for _, handle := range initiativeTaskHandles(initiative) {
		task, err := getTask(ctx, source, handle)
		if err != nil {
			return false, err
		}
		deliverySatisfied[handle] = task.State.SatisfiesInitiativeDependency()
	}
	for _, handle := range initiative.DependencyReadyTasks(deliverySatisfied) {
		if handle == integrationTask.Handle {
			return true, nil
		}
	}
	return false, nil
}
