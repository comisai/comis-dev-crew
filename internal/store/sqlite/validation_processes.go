package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/comisai/comis-dev-crew/internal/application"
	"github.com/comisai/comis-dev-crew/internal/domain"
	"github.com/comisai/comis-dev-crew/internal/validation"
)

// Record inserts or monotonically advances one service-owned validation process.
func (store *Store) Record(ctx context.Context, record validation.ProcessRecord) error {
	if store == nil || store.db == nil || ctx == nil {
		return errors.New("record validation process: store and context are required")
	}
	if err := record.Validate(); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin validation process record: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	previous, found, err := findValidationProcess(ctx, transaction, record.OperationID)
	if err != nil {
		return err
	}
	if !found {
		if record.State != validation.ProcessStarting {
			return errors.New("record validation process: initial state must be starting")
		}
		task, taskErr := getTask(ctx, transaction, record.TaskHandle)
		if taskErr != nil || task.State != domain.TaskValidating {
			return fmt.Errorf("record validation process: task is not validating: %w", application.ErrPrecondition)
		}
		if err := insertValidationProcess(ctx, transaction, record); err != nil {
			return err
		}
	} else {
		if err := record.CanFollow(previous); err != nil {
			return err
		}
		if err := updateValidationProcess(ctx, transaction, record); err != nil {
			return err
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit validation process record: %w", err)
	}
	return nil
}

// ListActiveValidationProcesses returns every non-exited record for startup reconciliation.
func (store *Store) ListActiveValidationProcesses(ctx context.Context) (records []validation.ProcessRecord, resultErr error) {
	if store == nil || store.db == nil || ctx == nil {
		return nil, errors.New("list validation processes: store and context are required")
	}
	const query = `SELECT
        task_handle, operation_id, program_id, executable_label, pid,
        start_identity, process_group_identity, state, started_at,
        observed_at, exit_code
    FROM validation_processes WHERE state NOT IN ('exited', 'absent')
    ORDER BY observed_at, operation_id`
	rows, err := store.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list validation processes: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, rows.Close()) }()
	for rows.Next() {
		record, scanErr := scanValidationProcess(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("list validation processes: %w", scanErr)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list validation processes: %w", err)
	}
	return records, nil
}

func findValidationProcess(ctx context.Context, source queryer, operationID string) (validation.ProcessRecord, bool, error) {
	const query = `SELECT
        task_handle, operation_id, program_id, executable_label, pid,
        start_identity, process_group_identity, state, started_at,
        observed_at, exit_code
    FROM validation_processes WHERE operation_id = ?`
	record, err := scanValidationProcess(source.QueryRowContext(ctx, query, operationID))
	if errors.Is(err, sql.ErrNoRows) {
		return validation.ProcessRecord{}, false, nil
	}
	if err != nil {
		return validation.ProcessRecord{}, false, fmt.Errorf("find validation process: %w", err)
	}
	return record, true, nil
}

func scanValidationProcess(row rowScanner) (validation.ProcessRecord, error) {
	var record validation.ProcessRecord
	var startedAt string
	var observedAt string
	var exitCode sql.NullInt64
	if err := row.Scan(
		&record.TaskHandle, &record.OperationID, &record.ProgramID, &record.ExecutableLabel,
		&record.PID, &record.StartIdentity, &record.ProcessGroupIdentity, &record.State,
		&startedAt, &observedAt, &exitCode,
	); err != nil {
		return validation.ProcessRecord{}, err
	}
	var err error
	record.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return validation.ProcessRecord{}, err
	}
	record.ObservedAt, err = parseTime(observedAt)
	if err != nil {
		return validation.ProcessRecord{}, err
	}
	if exitCode.Valid {
		value := int(exitCode.Int64)
		record.ExitCode = &value
	}
	if err := record.Validate(); err != nil {
		return validation.ProcessRecord{}, err
	}
	return record, nil
}

func insertValidationProcess(ctx context.Context, transaction *sql.Tx, record validation.ProcessRecord) error {
	const insert = `INSERT INTO validation_processes (
        task_handle, operation_id, program_id, executable_label, pid,
        start_identity, process_group_identity, state, started_at,
        observed_at, exit_code
    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := transaction.ExecContext(ctx, insert,
		record.TaskHandle, record.OperationID, record.ProgramID, record.ExecutableLabel,
		record.PID, record.StartIdentity, record.ProcessGroupIdentity, record.State,
		formatTime(record.StartedAt), formatTime(record.ObservedAt), record.ExitCode,
	); err != nil {
		if isConstraintError(err) {
			return fmt.Errorf("insert validation process: %w", application.ErrConflict)
		}
		return fmt.Errorf("insert validation process: %w", err)
	}
	return nil
}

func updateValidationProcess(ctx context.Context, transaction *sql.Tx, record validation.ProcessRecord) error {
	const update = `UPDATE validation_processes SET
        pid = ?, start_identity = ?, process_group_identity = ?, state = ?,
        observed_at = ?, exit_code = ? WHERE operation_id = ?`
	result, err := transaction.ExecContext(ctx, update,
		record.PID, record.StartIdentity, record.ProcessGroupIdentity, record.State,
		formatTime(record.ObservedAt), record.ExitCode, record.OperationID,
	)
	if err != nil {
		return fmt.Errorf("update validation process: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return errors.New("update validation process: exact process was not updated")
	}
	return nil
}
