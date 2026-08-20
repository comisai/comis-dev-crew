package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const commandPromoteBacklog = "PromoteBacklog"

// BacklogPromotionCommand completes the task contract for one ready request.
// Repository and shape are inherited from the durable item.
type BacklogPromotionCommand struct {
	OperationID        string
	ServiceInstanceID  string
	BacklogHandle      string
	BaseRevision       string
	AcceptanceCriteria []string
	Constraints        []string
	ValidationProfile  string
	DeliveryMode       domain.DeliveryMode
	WorkerProfileID    string
}

// BacklogPromotionReservation binds one request to one child preparation before
// any reversible workspace side effect begins.
type BacklogPromotionReservation struct {
	OperationID           string
	SubjectDigest         string
	BacklogHandle         string
	TaskOperationID       string
	ReservedAt            time.Time
	Item                  domain.BacklogItem
	SatisfiedDependencies []string
}

// BacklogPromotionMutation finalizes one already prepared child task.
type BacklogPromotionMutation struct {
	Reservation BacklogPromotionReservation
	Prepared    MutationResult
	At          time.Time
}

// BacklogPromotionResult returns the promoted item and normal private task join.
type BacklogPromotionResult struct {
	Item        domain.BacklogItem     `json:"item"`
	Task        domain.Task            `json:"task"`
	Preparation *ManagedRunPreparation `json:"-"`
	Operation   domain.OperationRecord `json:"-"`
}

// BacklogPromotionStore owns reservation, exact replay, and finalization.
type BacklogPromotionStore interface {
	ReplayBacklogPromotion(context.Context, string, string) (BacklogPromotionResult, bool, error)
	ReserveBacklogPromotion(context.Context, BacklogPromotionReservation) (BacklogPromotionReservation, error)
	CommitBacklogPromotion(context.Context, BacklogPromotionMutation) (BacklogPromotionResult, error)
}

// BacklogTaskPreparer is the normal two-phase task preparation path.
type BacklogTaskPreparer interface {
	PrepareTask(context.Context, PrepareTaskCommand) (MutationResult, error)
}

// BacklogPromotionConfig binds promotion to durable reservation and preparation.
type BacklogPromotionConfig struct {
	Store BacklogPromotionStore
	Tasks BacklogTaskPreparer
	Clock Clock
}

// BacklogPromotions converts ready request records into prepared tasks.
type BacklogPromotions struct {
	store BacklogPromotionStore
	tasks BacklogTaskPreparer
	clock Clock
}

// NewBacklogPromotions validates the promotion composition.
func NewBacklogPromotions(config BacklogPromotionConfig) (*BacklogPromotions, error) {
	if config.Store == nil || config.Tasks == nil || config.Clock == nil {
		return nil, errors.New("create backlog promotions: store, task preparation, and clock are required")
	}
	return &BacklogPromotions{store: config.Store, tasks: config.Tasks, clock: config.Clock}, nil
}

// PromoteBacklog reserves one ready item, prepares a normal task, and marks the
// item promoted only after the complete private preparation is durable.
func (promotions *BacklogPromotions) PromoteBacklog(
	ctx context.Context,
	command BacklogPromotionCommand,
) (BacklogPromotionResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return BacklogPromotionResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.BacklogHandle) != nil {
		return BacklogPromotionResult{}, mutationValidationFailure("backlog promotion identity is invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return BacklogPromotionResult{}, mutationValidationFailure("backlog promotion subject cannot be encoded")
	}
	if replay, found, err := promotions.store.ReplayBacklogPromotion(ctx, command.OperationID, subjectDigest); err != nil {
		return BacklogPromotionResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	reservation := BacklogPromotionReservation{
		OperationID: command.OperationID, SubjectDigest: subjectDigest,
		BacklogHandle:   command.BacklogHandle,
		TaskOperationID: backlogPromotionTaskOperationID(command.OperationID, command.BacklogHandle),
		ReservedAt:      promotions.clock().UTC(),
	}
	reservation, err = promotions.store.ReserveBacklogPromotion(ctx, reservation)
	if err != nil {
		return BacklogPromotionResult{}, mutationCommitFailure(err)
	}
	if err := validateBacklogPromotionReservation(reservation, command, subjectDigest); err != nil {
		return BacklogPromotionResult{}, &dependencyFailure{message: "backlog promotion reservation differs", cause: err}
	}
	prepareCommand := PrepareTaskCommand{
		OperationID: reservation.TaskOperationID, ServiceInstanceID: command.ServiceInstanceID,
		Shape: reservation.Item.Shape, RepositoryID: reservation.Item.RepositoryID,
		BaseRevision:       command.BaseRevision,
		AcceptanceCriteria: append([]string{reservation.Item.RequestedOutcome}, command.AcceptanceCriteria...),
		Constraints:        append([]string(nil), command.Constraints...), ValidationProfile: command.ValidationProfile,
		DeliveryMode: command.DeliveryMode, WorkerProfileID: command.WorkerProfileID,
	}
	prepared, err := promotions.tasks.PrepareTask(ctx, prepareCommand)
	if err != nil {
		return BacklogPromotionResult{}, err
	}
	if err := validateBacklogPreparedTask(reservation, prepared); err != nil {
		return BacklogPromotionResult{}, &dependencyFailure{message: "backlog task preparation differs", cause: err}
	}
	result, err := promotions.store.CommitBacklogPromotion(ctx, BacklogPromotionMutation{
		Reservation: reservation, Prepared: prepared, At: promotions.clock().UTC(),
	})
	if err != nil {
		return BacklogPromotionResult{}, mutationCommitFailure(err)
	}
	if err := validateBacklogPromotionResult(result, reservation); err != nil {
		return BacklogPromotionResult{}, &dependencyFailure{message: "backlog promotion result differs", cause: err}
	}
	return result, nil
}

func validateBacklogPromotionReservation(
	reservation BacklogPromotionReservation,
	command BacklogPromotionCommand,
	subjectDigest string,
) error {
	if reservation.OperationID != command.OperationID || reservation.SubjectDigest != subjectDigest ||
		reservation.BacklogHandle != command.BacklogHandle ||
		reservation.TaskOperationID != backlogPromotionTaskOperationID(command.OperationID, command.BacklogHandle) ||
		reservation.ReservedAt.Location() != time.UTC || reservation.Item.Handle != command.BacklogHandle ||
		len(reservation.SatisfiedDependencies) != len(reservation.Item.DependsOn) {
		return errors.New("reservation is invalid")
	}
	satisfied := make(map[string]bool, len(reservation.SatisfiedDependencies))
	for index, dependency := range reservation.SatisfiedDependencies {
		if dependency != reservation.Item.DependsOn[index] || satisfied[dependency] {
			return errors.New("reservation dependencies are invalid")
		}
		satisfied[dependency] = true
	}
	return reservation.Item.CheckPromotable(satisfied)
}

func validateBacklogPreparedTask(reservation BacklogPromotionReservation, prepared MutationResult) error {
	if prepared.Task.Handle == "" || prepared.Task.State != domain.TaskPrepared ||
		prepared.Task.RepositoryID != reservation.Item.RepositoryID || prepared.Task.Shape != reservation.Item.Shape ||
		prepared.Preparation == nil || prepared.Preparation.ExternalRunRef != prepared.Task.Handle ||
		prepared.Operation.ID != reservation.TaskOperationID || prepared.Operation.Command != commandPrepareTask ||
		prepared.Operation.Status != domain.OperationCompleted || prepared.Operation.ResultRef != prepared.Task.Handle {
		return errors.New("prepared task is invalid")
	}
	return nil
}

func validateBacklogPromotionResult(result BacklogPromotionResult, reservation BacklogPromotionReservation) error {
	if result.Item.Validate() != nil || result.Item.Handle != reservation.BacklogHandle ||
		result.Item.Readiness != domain.BacklogPromoted || result.Task.Handle == "" ||
		result.Preparation == nil || result.Preparation.ExternalRunRef != result.Task.Handle ||
		result.Operation.ID != reservation.OperationID || result.Operation.Command != commandPromoteBacklog ||
		result.Operation.SubjectDigest != reservation.SubjectDigest ||
		result.Operation.Status != domain.OperationCompleted || result.Operation.ResultRef != result.Task.Handle {
		return errors.New("promotion result is invalid")
	}
	return nil
}

func backlogPromotionTaskOperationID(operationID, backlogHandle string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(operationID+"\x00"+backlogHandle)))
	return "backlog-task-" + digest[:32]
}
