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

const commandPrepareInitiative = "PrepareInitiative"

const initiativePreparationMigration = `
CREATE TABLE initiative_preparations (
    initiative_handle TEXT PRIMARY KEY,
    registration_nonce TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    FOREIGN KEY(initiative_handle) REFERENCES initiatives(handle)
);
INSERT INTO schema_migrations(version, applied_at)
VALUES (35, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`

var _ application.InitiativeMutationStore = (*Store)(nil)

// ReplayInitiativePreparation returns the exact durable group result for an
// identical operation and audits altered reuse.
func (store *Store) ReplayInitiativePreparation(
	ctx context.Context,
	operationID string,
	subjectDigest string,
) (application.InitiativePreparationResult, bool, error) {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativePreparationResult{}, false, fmt.Errorf("begin initiative preparation replay: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	operation, found, err := mutationReplay(
		ctx, transaction, operationID, commandPrepareInitiative, subjectDigest,
	)
	if err != nil {
		return application.InitiativePreparationResult{}, false, commitReplayConflict(transaction, err)
	}
	if !found {
		return application.InitiativePreparationResult{}, false, nil
	}
	result, err := initiativePreparationResult(ctx, transaction, operation)
	if err != nil {
		return application.InitiativePreparationResult{}, false, err
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativePreparationResult{}, false, fmt.Errorf("commit initiative preparation replay: %w", err)
	}
	return result, true, nil
}

// CommitPreparedInitiative creates the initiative, member tasks, private joins,
// and replay outcomes atomically at one global state version.
func (store *Store) CommitPreparedInitiative(
	ctx context.Context,
	mutation application.PreparedInitiativeMutation,
) (application.InitiativePreparationResult, error) {
	if err := validatePreparedInitiativeMutation(mutation); err != nil {
		return application.InitiativePreparationResult{}, err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return application.InitiativePreparationResult{}, fmt.Errorf("begin prepared initiative mutation: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if operation, found, err := mutationReplay(
		ctx, transaction, mutation.OperationID, commandPrepareInitiative, mutation.SubjectDigest,
	); err != nil {
		return application.InitiativePreparationResult{}, commitReplayConflict(transaction, err)
	} else if found {
		result, err := initiativePreparationResult(ctx, transaction, operation)
		if err != nil {
			return application.InitiativePreparationResult{}, err
		}
		if err := transaction.Commit(); err != nil {
			return application.InitiativePreparationResult{}, fmt.Errorf("commit prepared initiative replay: %w", err)
		}
		return result, nil
	}
	stateVersion, err := nextMutationStateVersion(ctx, transaction)
	if err != nil {
		return application.InitiativePreparationResult{}, err
	}
	initiative := mutation.Initiative
	initiative.StateVersion = stateVersion
	if err := insertInitiative(ctx, transaction, initiative); err != nil {
		return application.InitiativePreparationResult{}, fmt.Errorf("insert prepared initiative: %w", err)
	}
	if err := insertInitiativePreparation(ctx, transaction, mutation); err != nil {
		return application.InitiativePreparationResult{}, fmt.Errorf("insert initiative preparation: %w", err)
	}
	for _, member := range mutation.Members {
		task := member.Task
		task.StateVersion = stateVersion
		preparedTask := application.PreparedTaskMutation{
			Task: task, Preparation: member.Preparation,
			OperationID: member.OperationID, SubjectDigest: member.SubjectDigest, At: mutation.At,
		}
		if err := consumeTaskPreparationIntent(ctx, transaction, preparedTask); err != nil {
			return application.InitiativePreparationResult{}, err
		}
		if err := insertTask(ctx, transaction, task); err != nil {
			return application.InitiativePreparationResult{}, fmt.Errorf("insert prepared initiative member: %w", err)
		}
		if err := insertManagedRunPreparation(ctx, transaction, task.Handle, member.Preparation, mutation.At); err != nil {
			return application.InitiativePreparationResult{}, fmt.Errorf("insert initiative member preparation: %w", err)
		}
		operation := completedMutationOperation(
			member.OperationID, commandPrepareTask, member.SubjectDigest,
			task.Handle, stateVersion, mutation.At,
		)
		if err := insertOperation(ctx, transaction, operation); err != nil {
			return application.InitiativePreparationResult{}, fmt.Errorf("insert initiative member operation: %w", err)
		}
	}
	operation := completedMutationOperation(
		mutation.OperationID, commandPrepareInitiative, mutation.SubjectDigest,
		initiative.Handle, stateVersion, mutation.At,
	)
	if err := insertOperation(ctx, transaction, operation); err != nil {
		return application.InitiativePreparationResult{}, fmt.Errorf("insert prepare initiative operation: %w", err)
	}
	result, err := initiativePreparationResult(ctx, transaction, operation)
	if err != nil {
		return application.InitiativePreparationResult{}, err
	}
	if err := transaction.Commit(); err != nil {
		return application.InitiativePreparationResult{}, fmt.Errorf("commit prepared initiative mutation: %w", err)
	}
	return result, nil
}

func validatePreparedInitiativeMutation(mutation application.PreparedInitiativeMutation) error {
	if mutation.Initiative.Validate() != nil || mutation.Initiative.State != domain.InitiativePreparing ||
		mutation.Initiative.ManagedRunGroupID != "" || !mutation.Initiative.CreatedAt.Equal(mutation.At) ||
		!mutation.Initiative.UpdatedAt.Equal(mutation.At) {
		return errors.New("commit prepared initiative: invalid initiative record")
	}
	preparations := make([]application.ManagedRunPreparation, 0, len(mutation.Members))
	baseRevisions := make(map[string]string, len(mutation.Initiative.BaseRevisionSet))
	for _, base := range mutation.Initiative.BaseRevisionSet {
		baseRevisions[base.RepositoryID] = base.Revision
	}
	type memberAuthority struct {
		repositoryID string
		baseRevision string
	}
	initiativeMembers := make(map[string]memberAuthority)
	for _, component := range mutation.Initiative.Components {
		for _, handle := range component.TaskHandles {
			initiativeMembers[handle] = memberAuthority{
				repositoryID: component.RepositoryID,
				baseRevision: baseRevisions[component.RepositoryID],
			}
		}
	}
	seenTasks := make(map[string]struct{}, len(mutation.Members))
	seenOperations := make(map[string]struct{}, len(mutation.Members))
	serviceInstanceID := ""
	for _, member := range mutation.Members {
		if member.Task.Validate() != nil || member.Task.State != domain.TaskPrepared ||
			!member.Task.CreatedAt.Equal(mutation.At) || !member.Task.UpdatedAt.Equal(mutation.At) ||
			member.Preparation.Validate(mutation.At) != nil ||
			member.Preparation.ExternalRunRef != member.Task.Handle ||
			domain.ValidateOperationID(member.OperationID) != nil || len(member.SubjectDigest) != 64 {
			return errors.New("commit prepared initiative: invalid member record")
		}
		authority, memberFound := initiativeMembers[member.Task.Handle]
		if !memberFound {
			return errors.New("commit prepared initiative: task is outside the initiative")
		}
		if member.Task.RepositoryID != authority.repositoryID || member.Task.BaseRevision != authority.baseRevision {
			return errors.New("commit prepared initiative: member repository authority differs")
		}
		if serviceInstanceID == "" {
			serviceInstanceID = member.Task.ServiceInstanceID
		} else if member.Task.ServiceInstanceID != serviceInstanceID {
			return errors.New("commit prepared initiative: member service authority differs")
		}
		if _, duplicate := seenTasks[member.Task.Handle]; duplicate {
			return errors.New("commit prepared initiative: duplicate member task")
		}
		if _, duplicate := seenOperations[member.OperationID]; duplicate {
			return errors.New("commit prepared initiative: duplicate member operation")
		}
		seenTasks[member.Task.Handle] = struct{}{}
		seenOperations[member.OperationID] = struct{}{}
		preparations = append(preparations, member.Preparation)
	}
	if len(seenTasks) != len(initiativeMembers) {
		return errors.New("commit prepared initiative: member set is incomplete")
	}
	group := application.ManagedRunGroupPreparation{
		ExternalGroupRef:  mutation.Initiative.Handle,
		RegistrationNonce: mutation.GroupRegistrationNonce,
		Members:           preparations, ExpiresAt: mutation.GroupExpiresAt,
	}
	if group.Validate(mutation.At) != nil || domain.ValidateOperationID(mutation.OperationID) != nil ||
		len(mutation.SubjectDigest) != 64 {
		return errors.New("commit prepared initiative: invalid group preparation")
	}
	return nil
}

func insertInitiativePreparation(
	ctx context.Context,
	target execer,
	mutation application.PreparedInitiativeMutation,
) error {
	const statement = `INSERT INTO initiative_preparations (
        initiative_handle, registration_nonce, expires_at, created_at
    ) VALUES (?, ?, ?, ?)`
	_, err := target.ExecContext(ctx, statement,
		mutation.Initiative.Handle, mutation.GroupRegistrationNonce,
		formatTime(mutation.GroupExpiresAt), formatTime(mutation.At),
	)
	return err
}

func initiativePreparationResult(
	ctx context.Context,
	source queryer,
	operation domain.OperationRecord,
) (application.InitiativePreparationResult, error) {
	initiative, err := getInitiative(ctx, source, operation.ResultRef)
	if err != nil {
		return application.InitiativePreparationResult{}, fmt.Errorf("read initiative preparation record: %w", err)
	}
	registrationNonce, expiresAt, createdAt, err := getInitiativePreparation(ctx, source, initiative.Handle)
	if err != nil {
		return application.InitiativePreparationResult{}, err
	}
	handles := initiativeTaskHandles(initiative)
	tasks := make([]domain.Task, 0, len(handles))
	preparations := make([]application.ManagedRunPreparation, 0, len(handles))
	for _, handle := range handles {
		task, err := getTask(ctx, source, handle)
		if err != nil {
			return application.InitiativePreparationResult{}, err
		}
		preparation, err := getManagedRunPreparation(ctx, source, task)
		if err != nil {
			return application.InitiativePreparationResult{}, err
		}
		tasks = append(tasks, task)
		preparations = append(preparations, preparation)
	}
	group := application.ManagedRunGroupPreparation{
		ExternalGroupRef: initiative.Handle, RegistrationNonce: registrationNonce,
		Members: preparations, ExpiresAt: expiresAt,
	}
	if group.Validate(createdAt) != nil {
		return application.InitiativePreparationResult{}, errors.New("stored initiative preparation is invalid")
	}
	return application.InitiativePreparationResult{
		Initiative: initiative, Tasks: tasks, Preparation: group, Operation: operation,
	}, nil
}

func getInitiativePreparation(
	ctx context.Context,
	source queryer,
	initiativeHandle string,
) (string, time.Time, time.Time, error) {
	const query = `SELECT registration_nonce, expires_at, created_at
        FROM initiative_preparations WHERE initiative_handle = ?`
	var nonce, expiresAtText, createdAtText string
	err := source.QueryRowContext(ctx, query, initiativeHandle).Scan(&nonce, &expiresAtText, &createdAtText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, time.Time{}, fmt.Errorf("get initiative preparation: %w", application.ErrNotFound)
	}
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("get initiative preparation: %w", err)
	}
	expiresAt, err := parseTime(expiresAtText)
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("parse initiative preparation expiry: %w", err)
	}
	createdAt, err := parseTime(createdAtText)
	if err != nil {
		return "", time.Time{}, time.Time{}, fmt.Errorf("parse initiative preparation creation: %w", err)
	}
	return nonce, expiresAt, createdAt, nil
}

func initiativeTaskHandles(initiative domain.DevelopmentInitiative) []string {
	handles := make([]string, 0)
	for _, component := range initiative.Components {
		handles = append(handles, component.TaskHandles...)
	}
	sort.Strings(handles)
	return handles
}
