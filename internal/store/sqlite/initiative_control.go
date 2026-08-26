package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
)

const initiativeControlMigration = `
CREATE TABLE initiative_group_controls (
    operation_id TEXT PRIMARY KEY,
    initiative_state TEXT NOT NULL,
	member_count INTEGER NOT NULL,
    FOREIGN KEY(operation_id) REFERENCES operations(id)
);
CREATE TABLE initiative_group_control_members (
    operation_id TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    task_handle TEXT NOT NULL,
    member_operation_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    error_code TEXT NOT NULL,
    task_state TEXT NOT NULL,
    state_version INTEGER NOT NULL,
    PRIMARY KEY(operation_id, task_handle),
    UNIQUE(operation_id, ordinal),
    UNIQUE(operation_id, member_operation_id),
    FOREIGN KEY(operation_id) REFERENCES operations(id),
    FOREIGN KEY(task_handle) REFERENCES tasks(handle)
);
INSERT INTO schema_migrations(version, applied_at)
VALUES (37, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

var _ application.InitiativeControlStore = (*Store)(nil)

// ReplayInitiativeControl returns the exact original per-member result.
func (store *Store) ReplayInitiativeControl(
	ctx context.Context,
	operationID, command, subjectDigest string,
) (application.InitiativeControlResult, bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativeControlResult{}, false, fmt.Errorf("begin initiative control replay: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(ctx, transaction, operationID, command, subjectDigest)
	if err != nil {
		return application.InitiativeControlResult{}, false, commitReplayConflict(transaction, err)
	}
	if !found {
		return application.InitiativeControlResult{}, false, nil
	}
	result, err := readInitiativeControlResult(ctx, transaction, operation)
	if err != nil {
		return application.InitiativeControlResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativeControlResult{}, false, fmt.Errorf("commit initiative control replay: %w", err)
	}
	return result, true, nil
}

// CommitInitiativeControl records one final group result without changing members.
func (store *Store) CommitInitiativeControl(
	ctx context.Context,
	mutation application.InitiativeControlMutation,
) (application.InitiativeControlResult, error) {
	if err := validateInitiativeControlMutation(mutation); err != nil {
		return application.InitiativeControlResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("begin initiative control: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if operation, found, err := mutationReplay(
		ctx, transaction, mutation.OperationID, mutation.Command, mutation.SubjectDigest,
	); err != nil {
		return application.InitiativeControlResult{}, commitReplayConflict(transaction, err)
	} else if found {
		return readInitiativeControlResult(ctx, transaction, operation)
	}
	if err := verifyInitiativeControlSnapshot(ctx, transaction, mutation.Command, mutation.Result); err != nil {
		return application.InitiativeControlResult{}, err
	}
	operation := completedMutationOperation(
		mutation.OperationID, mutation.Command, mutation.SubjectDigest,
		mutation.Result.InitiativeHandle, mutation.Result.StateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("insert initiative control operation: %w", err)
	}
	if _, err := transaction.ExecContext(ctx,
		"INSERT INTO initiative_group_controls(operation_id, initiative_state, member_count) VALUES (?, ?, ?)",
		mutation.OperationID, mutation.Result.State, len(mutation.Result.Members),
	); err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("insert initiative control result: %w", err)
	}
	for index, member := range mutation.Result.Members {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO initiative_group_control_members(
            operation_id, ordinal, task_handle, member_operation_id, outcome,
            error_code, task_state, state_version
        ) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			mutation.OperationID, index, member.TaskHandle, member.OperationID,
			member.Outcome, member.ErrorCode, member.State, member.StateVersion,
		); err != nil {
			return application.InitiativeControlResult{}, fmt.Errorf("insert initiative control member: %w", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("commit initiative control: %w", err)
	}
	result := mutation.Result
	result.Operation = operation
	return result, nil
}

func verifyInitiativeControlSnapshot(
	ctx context.Context,
	transaction *sql.Tx,
	command string,
	result application.InitiativeControlResult,
) error {
	initiative, err := getInitiative(ctx, transaction, result.InitiativeHandle)
	if err != nil {
		return err
	}
	stateVersion, err := currentStateVersion(ctx, transaction)
	if err != nil {
		return err
	}
	if initiative.State != result.State || stateVersion != result.StateVersion {
		return fmt.Errorf("initiative control snapshot changed: %w", application.ErrPrecondition)
	}
	handles := initiativeTaskHandles(initiative)
	if len(handles) != len(result.Members) {
		return fmt.Errorf("initiative control member set changed: %w", application.ErrPrecondition)
	}
	for index, handle := range handles {
		member := result.Members[index]
		task, taskErr := getTask(ctx, transaction, handle)
		if taskErr != nil {
			return taskErr
		}
		if member.TaskHandle != handle || member.State != task.State || member.StateVersion != task.StateVersion {
			return fmt.Errorf("initiative control member changed: %w", application.ErrPrecondition)
		}
		if member.Outcome == application.InitiativeControlCompleted {
			operation, operationErr := getOperation(ctx, transaction, member.OperationID)
			if operationErr != nil || operation.Command != initiativeMemberCommand(command) ||
				operation.ResultRef != member.TaskHandle || operation.StateVersion != member.StateVersion {
				return fmt.Errorf("initiative control member operation is missing: %w", application.ErrPrecondition)
			}
		}
	}
	return nil
}

func readInitiativeControlResult(
	ctx context.Context,
	source queryer,
	operation domain.OperationRecord,
) (application.InitiativeControlResult, error) {
	var state domain.InitiativeState
	var memberCount int
	if err := source.QueryRowContext(ctx,
		"SELECT initiative_state, member_count FROM initiative_group_controls WHERE operation_id = ?", operation.ID,
	).Scan(&state, &memberCount); err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("read initiative control result: %w", err)
	}
	rows, err := source.QueryContext(ctx, `SELECT task_handle, member_operation_id, outcome,
        error_code, task_state, state_version
        FROM initiative_group_control_members WHERE operation_id = ? ORDER BY ordinal`, operation.ID)
	if err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("read initiative control members: %w", err)
	}
	defer func() { _ = rows.Close() }()
	members := make([]application.InitiativeControlMemberResult, 0)
	for rows.Next() {
		var member application.InitiativeControlMemberResult
		if err := rows.Scan(
			&member.TaskHandle, &member.OperationID, &member.Outcome,
			&member.ErrorCode, &member.State, &member.StateVersion,
		); err != nil {
			return application.InitiativeControlResult{}, fmt.Errorf("scan initiative control member: %w", err)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return application.InitiativeControlResult{}, fmt.Errorf("read initiative control members: %w", err)
	}
	if memberCount != len(members) {
		return application.InitiativeControlResult{}, errors.New("initiative control replay member set is incomplete")
	}
	result := application.InitiativeControlResult{
		InitiativeHandle: operation.ResultRef, State: state,
		StateVersion: operation.StateVersion, Members: members, Operation: operation,
	}
	if err := validateInitiativeControlResult(result); err != nil {
		return application.InitiativeControlResult{}, err
	}
	return result, nil
}

func validateInitiativeControlMutation(mutation application.InitiativeControlMutation) error {
	if domain.ValidateOperationID(mutation.OperationID) != nil || len(mutation.SubjectDigest) != 64 ||
		mutation.At.Location() != time.UTC || !validInitiativeControlCommand(mutation.Command) ||
		mutation.Result.Operation.ID != "" {
		return errors.New("initiative control mutation is invalid")
	}
	return validateInitiativeControlResult(mutation.Result)
}

func validateInitiativeControlResult(result application.InitiativeControlResult) error {
	if domain.ValidateTaskHandle(result.InitiativeHandle) != nil ||
		domain.ValidateInitiativeState(result.State) != nil || result.StateVersion < 1 ||
		len(result.Members) == 0 || len(result.Members) > 64 {
		return errors.New("initiative control result is invalid")
	}
	seenTasks := make(map[string]struct{}, len(result.Members))
	seenOperations := make(map[string]struct{}, len(result.Members))
	for _, member := range result.Members {
		if domain.ValidateTaskHandle(member.TaskHandle) != nil || domain.ValidateOperationID(member.OperationID) != nil ||
			domain.ValidateTaskState(member.State) != nil || member.StateVersion < 1 ||
			!validInitiativeControlOutcome(member.Outcome, member.ErrorCode) {
			return errors.New("initiative control member result is invalid")
		}
		if _, exists := seenTasks[member.TaskHandle]; exists {
			return errors.New("initiative control member task is duplicated")
		}
		if _, exists := seenOperations[member.OperationID]; exists {
			return errors.New("initiative control member operation is duplicated")
		}
		seenTasks[member.TaskHandle], seenOperations[member.OperationID] = struct{}{}, struct{}{}
	}
	if !sort.SliceIsSorted(result.Members, func(left, right int) bool {
		return result.Members[left].TaskHandle < result.Members[right].TaskHandle
	}) {
		return errors.New("initiative control members are not ordered")
	}
	return nil
}

func validInitiativeControlCommand(command string) bool {
	return command == "PauseInitiative" || command == "ResumeInitiative" || command == "CancelInitiative"
}

func initiativeMemberCommand(command string) string {
	switch command {
	case "PauseInitiative":
		return commandPauseTask
	case "ResumeInitiative":
		return commandResumeTask
	case "CancelInitiative":
		return commandCancelTask
	default:
		return ""
	}
}

func validInitiativeControlOutcome(outcome application.InitiativeControlOutcome, code domain.ErrorCode) bool {
	switch outcome {
	case application.InitiativeControlCompleted:
		return code == ""
	case application.InitiativeControlRejected, application.InitiativeControlUnknown,
		application.InitiativeControlNotAttempted:
		return code.Valid()
	default:
		return false
	}
}
