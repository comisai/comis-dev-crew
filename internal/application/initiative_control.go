package application

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/domain"
)

const (
	commandPauseInitiative  = "PauseInitiative"
	commandResumeInitiative = "ResumeInitiative"
	commandCancelInitiative = "CancelInitiative"
)

// InitiativeControlOutcome is the closed result vocabulary for one member.
type InitiativeControlOutcome string

const (
	InitiativeControlCompleted    InitiativeControlOutcome = "completed"
	InitiativeControlRejected     InitiativeControlOutcome = "rejected"
	InitiativeControlUnknown      InitiativeControlOutcome = "unknown"
	InitiativeControlNotAttempted InitiativeControlOutcome = "not_attempted"
)

// InitiativeControlCommand names one group without selecting member authority.
type InitiativeControlCommand struct {
	OperationID      string `json:"operationId"`
	InitiativeHandle string `json:"initiativeHandle"`
}

// InitiativeControlMemberResult preserves one independently applied outcome.
type InitiativeControlMemberResult struct {
	TaskHandle   string                   `json:"taskHandle"`
	OperationID  string                   `json:"operationId"`
	Outcome      InitiativeControlOutcome `json:"outcome"`
	ErrorCode    domain.ErrorCode         `json:"errorCode,omitempty"`
	State        domain.TaskState         `json:"state"`
	StateVersion int64                    `json:"stateVersion"`
}

// InitiativeControlResult is the exact durable group-operation projection.
type InitiativeControlResult struct {
	InitiativeHandle string                          `json:"initiativeHandle"`
	State            domain.InitiativeState          `json:"state"`
	StateVersion     int64                           `json:"stateVersion"`
	Members          []InitiativeControlMemberResult `json:"members"`
	Operation        domain.OperationRecord          `json:"-"`
}

// InitiativeControlMutation records the final group result after member calls.
type InitiativeControlMutation struct {
	OperationID   string
	Command       string
	SubjectDigest string
	Result        InitiativeControlResult
	At            time.Time
}

// InitiativeControlStore owns exact group replay and authoritative snapshots.
type InitiativeControlStore interface {
	ReplayInitiativeControl(context.Context, string, string, string) (InitiativeControlResult, bool, error)
	InitiativeObservation(context.Context, string) (domain.DevelopmentInitiative, []domain.Task, int64, error)
	CommitInitiativeControl(context.Context, InitiativeControlMutation) (InitiativeControlResult, error)
}

// InitiativeTaskControls is the existing task mutation path used per member.
type InitiativeTaskControls interface {
	PauseTask(context.Context, PauseTaskCommand) (MutationResult, error)
	CancelTask(context.Context, CancelTaskCommand) (MutationResult, error)
}

// InitiativeTaskResumer is optional because resume requires workspace inspection.
type InitiativeTaskResumer interface {
	ResumeTask(context.Context, ResumeTaskCommand) (MutationResult, error)
}

// InitiativeControlConfig binds group coordination to existing task controls.
type InitiativeControlConfig struct {
	Store   InitiativeControlStore
	Tasks   InitiativeTaskControls
	Resumer InitiativeTaskResumer
	Clock   Clock
}

// InitiativeControls coordinates non-atomic per-member control operations.
type InitiativeControls struct {
	store   InitiativeControlStore
	tasks   InitiativeTaskControls
	resumer InitiativeTaskResumer
	clock   Clock
}

// NewInitiativeControls validates the group-control composition.
func NewInitiativeControls(config InitiativeControlConfig) (*InitiativeControls, error) {
	if config.Store == nil || config.Tasks == nil || config.Clock == nil {
		return nil, errors.New("create initiative controls: store, tasks, and clock are required")
	}
	return &InitiativeControls{
		store: config.Store, tasks: config.Tasks, resumer: config.Resumer, clock: config.Clock,
	}, nil
}

// PauseInitiative asks every current member to reach its own safe boundary.
func (controls *InitiativeControls) PauseInitiative(
	ctx context.Context,
	command InitiativeControlCommand,
) (InitiativeControlResult, error) {
	return controls.control(ctx, commandPauseInitiative, command)
}

// ResumeInitiative resumes each member that independently passes resume checks.
func (controls *InitiativeControls) ResumeInitiative(
	ctx context.Context,
	command InitiativeControlCommand,
) (InitiativeControlResult, error) {
	return controls.control(ctx, commandResumeInitiative, command)
}

// CancelInitiative cancels every current member while preserving artifacts.
func (controls *InitiativeControls) CancelInitiative(
	ctx context.Context,
	command InitiativeControlCommand,
) (InitiativeControlResult, error) {
	return controls.control(ctx, commandCancelInitiative, command)
}

func (controls *InitiativeControls) control(
	ctx context.Context,
	commandName string,
	command InitiativeControlCommand,
) (InitiativeControlResult, error) {
	if err := validMutationContext(ctx); err != nil {
		return InitiativeControlResult{}, err
	}
	if domain.ValidateOperationID(command.OperationID) != nil ||
		domain.ValidateTaskHandle(command.InitiativeHandle) != nil {
		return InitiativeControlResult{}, mutationValidationFailure("initiative control fields are invalid")
	}
	subjectDigest, err := digestMutationSubject(command)
	if err != nil {
		return InitiativeControlResult{}, mutationValidationFailure("initiative control subject cannot be encoded")
	}
	if replay, found, err := controls.store.ReplayInitiativeControl(
		ctx, command.OperationID, commandName, subjectDigest,
	); err != nil {
		return InitiativeControlResult{}, mutationReplayFailure(err)
	} else if found {
		return replay, nil
	}
	if commandName == commandResumeInitiative && controls.resumer == nil {
		return InitiativeControlResult{}, initiativeResumeUnavailable()
	}
	initiative, tasks, _, err := controls.store.InitiativeObservation(ctx, command.InitiativeHandle)
	if err != nil {
		return InitiativeControlResult{}, translateReadError(err, "initiative")
	}
	sort.Slice(tasks, func(left, right int) bool { return tasks[left].Handle < tasks[right].Handle })
	members := controls.controlMembers(ctx, commandName, command.OperationID, tasks)
	if err := ctx.Err(); err != nil {
		return InitiativeControlResult{}, err
	}
	initiative, currentTasks, stateVersion, err := controls.store.InitiativeObservation(ctx, initiative.Handle)
	if err != nil {
		return InitiativeControlResult{}, mutationCommitFailure(err)
	}
	if err := refreshInitiativeControlMembers(members, currentTasks); err != nil {
		return InitiativeControlResult{}, mutationCommitFailure(err)
	}
	result := InitiativeControlResult{
		InitiativeHandle: initiative.Handle, State: initiative.State,
		StateVersion: stateVersion, Members: members,
	}
	return controls.store.CommitInitiativeControl(ctx, InitiativeControlMutation{
		OperationID: command.OperationID, Command: commandName, SubjectDigest: subjectDigest,
		Result: result, At: controls.clock().UTC(),
	})
}

func (controls *InitiativeControls) controlMembers(
	ctx context.Context,
	commandName, operationID string,
	tasks []domain.Task,
) []InitiativeControlMemberResult {
	members := make([]InitiativeControlMemberResult, 0, len(tasks))
	for _, task := range tasks {
		memberOperationID := initiativeControlMemberOperationID(operationID, commandName, task.Handle)
		member := InitiativeControlMemberResult{
			TaskHandle: task.Handle, OperationID: memberOperationID,
			State: task.State, StateVersion: task.StateVersion,
		}
		if ctx.Err() != nil {
			member.Outcome, member.ErrorCode = InitiativeControlNotAttempted, domain.ErrorUnknown
			members = append(members, member)
			continue
		}
		result, err := controls.controlMember(ctx, commandName, memberOperationID, task.Handle)
		if err == nil {
			member.Outcome = InitiativeControlCompleted
			member.State, member.StateVersion = result.Task.State, result.Task.StateVersion
		} else {
			member.Outcome, member.ErrorCode = classifyInitiativeControlFailure(err)
		}
		members = append(members, member)
	}
	return members
}

func (controls *InitiativeControls) controlMember(
	ctx context.Context,
	commandName, operationID, taskHandle string,
) (MutationResult, error) {
	switch commandName {
	case commandPauseInitiative:
		return controls.tasks.PauseTask(ctx, PauseTaskCommand{OperationID: operationID, TaskHandle: taskHandle})
	case commandResumeInitiative:
		return controls.resumer.ResumeTask(ctx, ResumeTaskCommand{OperationID: operationID, TaskHandle: taskHandle})
	case commandCancelInitiative:
		return controls.tasks.CancelTask(ctx, CancelTaskCommand{OperationID: operationID, TaskHandle: taskHandle})
	default:
		return MutationResult{}, errors.New("unknown initiative control command")
	}
}

func refreshInitiativeControlMembers(
	members []InitiativeControlMemberResult,
	tasks []domain.Task,
) error {
	current := make(map[string]domain.Task, len(tasks))
	for _, task := range tasks {
		current[task.Handle] = task
	}
	if len(current) != len(members) {
		return ErrPrecondition
	}
	for index := range members {
		task, found := current[members[index].TaskHandle]
		if !found {
			return ErrPrecondition
		}
		members[index].State, members[index].StateVersion = task.State, task.StateVersion
	}
	return nil
}

func classifyInitiativeControlFailure(err error) (InitiativeControlOutcome, domain.ErrorCode) {
	var failure *domain.Failure
	if !errors.As(err, &failure) {
		switch {
		case errors.Is(err, ErrConflict):
			return InitiativeControlRejected, domain.ErrorConflict
		case errors.Is(err, ErrInvalidInput):
			return InitiativeControlRejected, domain.ErrorInvalidArgument
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrPrecondition),
			errors.Is(err, domain.ErrInvalidTransition):
			return InitiativeControlRejected, domain.ErrorPrecondition
		}
		return InitiativeControlUnknown, domain.ErrorUnknown
	}
	if failure.Retryable || failure.Code == domain.ErrorUnavailable ||
		failure.Code == domain.ErrorDeadlineExceeded || failure.Code == domain.ErrorInternal ||
		failure.Code == domain.ErrorUnknown {
		return InitiativeControlUnknown, failure.Code
	}
	return InitiativeControlRejected, failure.Code
}

func initiativeControlMemberOperationID(parent, commandName, taskHandle string) string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(parent+"\x00"+commandName+"\x00"+taskHandle)))
	return "control-member-" + digest[:32]
}

func initiativeResumeUnavailable() error {
	failure, err := domain.NewFailure(
		domain.ErrorUnavailable, true,
		"initiative resume is unavailable without workspace inspection",
		"configure the reviewed workspace inspector and retry",
		nil,
	)
	if err != nil {
		return errors.New("initiative resume failure cannot be encoded")
	}
	return failure
}
