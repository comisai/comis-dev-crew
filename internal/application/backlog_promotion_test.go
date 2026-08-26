package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

func TestBacklogPromotionReservesBeforeNormalPreparationAndPreservesOutcome(t *testing.T) {
	item := queryBacklogItem("backlog-promote", "repo-primary", domain.BacklogReady)
	item.DependsOn = []string{"backlog-dependency"}
	store := &backlogPromotionStoreStub{item: item}
	tasks := &backlogTaskPreparerStub{}
	promotions, err := NewBacklogPromotions(BacklogPromotionConfig{
		Store: store, Tasks: tasks, Clock: backlogAdditionClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	command := validBacklogPromotionCommand()
	result, err := promotions.PromoteBacklog(context.Background(), command)
	if err != nil {
		t.Fatalf("PromoteBacklog() error = %v", err)
	}
	if store.reserveCalls != 1 || tasks.calls != 1 || store.commitCalls != 1 {
		t.Fatalf("reserve/prepare/commit calls = %d/%d/%d", store.reserveCalls, tasks.calls, store.commitCalls)
	}
	if tasks.command.OperationID == command.OperationID || domain.ValidateOperationID(tasks.command.OperationID) != nil ||
		tasks.command.RepositoryID != item.RepositoryID || tasks.command.Shape != item.Shape ||
		tasks.command.ServiceInstanceID != command.ServiceInstanceID ||
		len(tasks.command.AcceptanceCriteria) != 2 || tasks.command.AcceptanceCriteria[0] != item.RequestedOutcome ||
		tasks.command.AcceptanceCriteria[1] != command.AcceptanceCriteria[0] {
		t.Fatalf("normal preparation command = %#v", tasks.command)
	}
	if result.Item.Readiness != domain.BacklogPromoted || result.Task.Handle != "task-backlog-promoted" ||
		result.Preparation == nil || result.Preparation.ExternalRunRef != result.Task.Handle ||
		result.Operation.ID != command.OperationID || result.Operation.Command != "PromoteBacklog" {
		t.Fatalf("PromoteBacklog() = %#v", result)
	}
}

func TestBacklogPromotionReplaysBeforeReservationOrTaskPreparation(t *testing.T) {
	item := queryBacklogItem("backlog-replay", "repo-primary", domain.BacklogPromoted)
	replay := backlogPromotionResult(item, "operation-promote-backlog")
	store := &backlogPromotionStoreStub{replay: &replay}
	tasks := &backlogTaskPreparerStub{}
	promotions, err := NewBacklogPromotions(BacklogPromotionConfig{
		Store: store, Tasks: tasks, Clock: backlogAdditionClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := promotions.PromoteBacklog(context.Background(), validBacklogPromotionCommand())
	if err != nil || result.Task.Handle != replay.Task.Handle {
		t.Fatalf("PromoteBacklog(replay) = %#v, %v", result, err)
	}
	if store.reserveCalls != 0 || tasks.calls != 0 || store.commitCalls != 0 {
		t.Fatalf("replay repeated reserve/prepare/commit = %d/%d/%d", store.reserveCalls, tasks.calls, store.commitCalls)
	}
}

func TestBacklogPromotionFailsBeforePreparationWhenReservationIsRefused(t *testing.T) {
	store := &backlogPromotionStoreStub{reserveErr: ErrPrecondition}
	tasks := &backlogTaskPreparerStub{}
	promotions, err := NewBacklogPromotions(BacklogPromotionConfig{
		Store: store, Tasks: tasks, Clock: backlogAdditionClock,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = promotions.PromoteBacklog(context.Background(), validBacklogPromotionCommand())
	var failure *domain.Failure
	if !errors.As(err, &failure) || failure.Code != domain.ErrorPrecondition {
		t.Fatalf("PromoteBacklog(refused reservation) error = %#v", err)
	}
	if tasks.calls != 0 || store.commitCalls != 0 {
		t.Fatalf("refused reservation prepared/committed = %d/%d", tasks.calls, store.commitCalls)
	}
	if _, err := NewBacklogPromotions(BacklogPromotionConfig{}); err == nil {
		t.Fatal("NewBacklogPromotions(empty) error = nil")
	}
}

func TestBacklogPromotionRejectsDivergentDependencyResults(t *testing.T) {
	item := queryBacklogItem("backlog-promote", "repo-primary", domain.BacklogReady)
	newCoordinator := func(store *backlogPromotionStoreStub, tasks *backlogTaskPreparerStub) *BacklogPromotions {
		t.Helper()
		store.item = item
		promotions, err := NewBacklogPromotions(BacklogPromotionConfig{
			Store: store, Tasks: tasks, Clock: backlogAdditionClock,
		})
		if err != nil {
			t.Fatal(err)
		}
		return promotions
	}
	t.Run("replay dependency failure stops reservation", func(t *testing.T) {
		store := &backlogPromotionStoreStub{replayErr: ErrConflict}
		tasks := &backlogTaskPreparerStub{}
		if _, err := newCoordinator(store, tasks).PromoteBacklog(
			context.Background(), validBacklogPromotionCommand(),
		); err == nil || store.reserveCalls != 0 || tasks.calls != 0 {
			t.Fatalf("PromoteBacklog(replay failure) calls = %d/%d, error %v", store.reserveCalls, tasks.calls, err)
		}
	})
	t.Run("altered reservation stops task preparation", func(t *testing.T) {
		store := &backlogPromotionStoreStub{reservationMutation: func(reservation *BacklogPromotionReservation) {
			reservation.TaskOperationID = "backlog-task-substituted"
		}}
		tasks := &backlogTaskPreparerStub{}
		if _, err := newCoordinator(store, tasks).PromoteBacklog(
			context.Background(), validBacklogPromotionCommand(),
		); err == nil || tasks.calls != 0 || store.commitCalls != 0 {
			t.Fatalf("PromoteBacklog(altered reservation) calls = %d/%d, error %v", tasks.calls, store.commitCalls, err)
		}
	})
	t.Run("altered prepared task stops finalization", func(t *testing.T) {
		store := &backlogPromotionStoreStub{}
		tasks := &backlogTaskPreparerStub{resultMutation: func(result *MutationResult) {
			result.Task.RepositoryID = "repository-substituted"
		}}
		if _, err := newCoordinator(store, tasks).PromoteBacklog(
			context.Background(), validBacklogPromotionCommand(),
		); err == nil || store.commitCalls != 0 {
			t.Fatalf("PromoteBacklog(altered task) commits = %d, error %v", store.commitCalls, err)
		}
	})
	t.Run("altered final result fails closed", func(t *testing.T) {
		store := &backlogPromotionStoreStub{resultMutation: func(result *BacklogPromotionResult) {
			result.Operation.ResultRef = "task-substituted"
		}}
		if _, err := newCoordinator(store, &backlogTaskPreparerStub{}).PromoteBacklog(
			context.Background(), validBacklogPromotionCommand(),
		); err == nil {
			t.Fatal("PromoteBacklog(altered final result) error = nil")
		}
	})
	invalid := validBacklogPromotionCommand()
	invalid.OperationID = ""
	if _, err := newCoordinator(&backlogPromotionStoreStub{}, &backlogTaskPreparerStub{}).PromoteBacklog(
		context.Background(), invalid,
	); err == nil {
		t.Fatal("PromoteBacklog(invalid identity) error = nil")
	}
}

type backlogPromotionStoreStub struct {
	item                domain.BacklogItem
	replay              *BacklogPromotionResult
	replayErr           error
	reserveErr          error
	reservationMutation func(*BacklogPromotionReservation)
	resultMutation      func(*BacklogPromotionResult)
	reserveCalls        int
	commitCalls         int
}

func (store *backlogPromotionStoreStub) ReplayBacklogPromotion(
	context.Context,
	string,
	string,
) (BacklogPromotionResult, bool, error) {
	if store.replayErr != nil {
		return BacklogPromotionResult{}, false, store.replayErr
	}
	if store.replay == nil {
		return BacklogPromotionResult{}, false, nil
	}
	return *store.replay, true, nil
}

func (store *backlogPromotionStoreStub) ReserveBacklogPromotion(
	_ context.Context,
	reservation BacklogPromotionReservation,
) (BacklogPromotionReservation, error) {
	store.reserveCalls++
	if store.reserveErr != nil {
		return BacklogPromotionReservation{}, store.reserveErr
	}
	reservation.Item = store.item
	reservation.SatisfiedDependencies = append([]string(nil), store.item.DependsOn...)
	if store.reservationMutation != nil {
		store.reservationMutation(&reservation)
	}
	return reservation, nil
}

func (store *backlogPromotionStoreStub) CommitBacklogPromotion(
	_ context.Context,
	mutation BacklogPromotionMutation,
) (BacklogPromotionResult, error) {
	store.commitCalls++
	item := mutation.Reservation.Item
	item.Readiness = domain.BacklogPromoted
	item.UpdatedAt = mutation.At
	result := backlogPromotionResult(item, mutation.Reservation.OperationID)
	result.Task = mutation.Prepared.Task
	result.Preparation = mutation.Prepared.Preparation
	result.Operation.SubjectDigest = mutation.Reservation.SubjectDigest
	if store.resultMutation != nil {
		store.resultMutation(&result)
	}
	return result, nil
}

type backlogTaskPreparerStub struct {
	calls          int
	command        PrepareTaskCommand
	resultMutation func(*MutationResult)
}

func (tasks *backlogTaskPreparerStub) PrepareTask(
	_ context.Context,
	command PrepareTaskCommand,
) (MutationResult, error) {
	tasks.calls++
	tasks.command = command
	preparedAt := backlogAdditionClock()
	task := domain.Task{
		SchemaVersion: 1, Handle: "task-backlog-promoted", ServiceInstanceID: command.ServiceInstanceID,
		State: domain.TaskPrepared, Shape: command.Shape, RepositoryID: command.RepositoryID,
		BaseRevision: command.BaseRevision, BriefRevision: 1,
		AcceptanceCriteria: append([]string(nil), command.AcceptanceCriteria...),
		Constraints:        append([]string(nil), command.Constraints...), ValidationProfile: command.ValidationProfile,
		DeliveryMode: command.DeliveryMode, WorkerProfileID: command.WorkerProfileID,
		StateVersion: 8, CreatedAt: preparedAt, UpdatedAt: preparedAt,
	}
	preparation := &ManagedRunPreparation{
		ExternalRunRef: task.Handle, RegistrationNonce: "registration-nonce_backlog",
		RequestedAttachment: PreparedRuntimeAttachment{
			Kind: RuntimeAttachmentUnixSocket, SourcePath: "/approved/runtime/task-backlog-promoted/attachment.sock",
			RelayIdentity: "abababababababababababababababababababababababababababababababab",
		},
		ExpiresAt: preparedAt.Add(time.Hour), State: PreparationOpen,
	}
	result := MutationResult{
		Task: task, Preparation: preparation,
		Operation: completedBacklogOperation(command.OperationID, "PrepareTask", "", task.Handle, preparedAt),
	}
	if tasks.resultMutation != nil {
		tasks.resultMutation(&result)
	}
	return result, nil
}

func validBacklogPromotionCommand() BacklogPromotionCommand {
	return BacklogPromotionCommand{
		OperationID: "operation-promote-backlog", ServiceInstanceID: "service-instance-0001",
		BacklogHandle: "backlog-promote", BaseRevision: "0123456789abcdef0123456789abcdef01234567",
		AcceptanceCriteria: []string{"The implementation is verified."}, Constraints: []string{},
		ValidationProfile: "go-default", DeliveryMode: domain.DeliveryPullRequest,
		WorkerProfileID: "codex-reviewed",
	}
}

func backlogPromotionResult(item domain.BacklogItem, operationID string) BacklogPromotionResult {
	preparedAt := backlogAdditionClock()
	task := domain.Task{Handle: "task-backlog-promoted", State: domain.TaskPrepared, StateVersion: 8}
	preparation := &ManagedRunPreparation{ExternalRunRef: task.Handle}
	return BacklogPromotionResult{
		Item: item, Task: task, Preparation: preparation,
		Operation: completedBacklogOperation(operationID, "PromoteBacklog", "", task.Handle, preparedAt),
	}
}
