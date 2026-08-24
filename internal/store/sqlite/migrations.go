package sqlite

import (
	"context"
	"fmt"
)

func (store *Store) migrate(ctx context.Context) error {
	migrations := []struct {
		version int
		script  string
	}{
		{script: initialMigration}, {script: recordMigration},
		{version: 3, script: taskContractMigration}, {version: 4, script: taskBindingMigration},
		{version: 5, script: reportMigration}, {version: 6, script: managedRunPreparationMigration},
	}
	for _, migration := range migrations {
		var err error
		if migration.version == 0 {
			err = store.applyMigration(ctx, migration.script)
		} else {
			err = store.applyVersionedMigration(ctx, migration.version, migration.script)
		}
		if err != nil {
			return err
		}
	}
	if err := store.applyComisReportOutboxMigration(ctx); err != nil {
		return err
	}
	versioned := []struct {
		version int
		script  string
	}{
		{8, managedRunLifecycleMigration}, {9, terminalLifecycleMigration},
		{10, runtimeAttachmentMigration}, {11, activatedAttachmentMigration},
		{12, validationProcessMigration}, {13, candidateEvidenceMigration},
		{14, comisEvidenceOutboxMigration}, {15, taskHandbackMigration},
		{16, taskCleanupMigration}, {17, taskCandidateReconciliationMigration},
	}
	for _, migration := range versioned {
		if err := store.applyVersionedMigration(ctx, migration.version, migration.script); err != nil {
			return err
		}
	}
	if err := store.applyTaskPreparationMigrations(ctx); err != nil {
		return err
	}
	if err := store.applyRuntimeRelayUpgradeMigration(ctx); err != nil {
		return err
	}
	remaining := []struct {
		version int
		script  string
	}{
		{21, runtimeRelayRefusalMigration}, {22, runtimeAttachmentRecoveryRefusalMigration},
		{23, taskPauseRequestMigration}, {24, scoutPromotionMigration},
		{25, taskReplacementMigration}, {26, taskSteeringMigration},
		{27, taskDiscardMigration}, {28, scoutAttestationMigration},
		{29, decisionSurfacingMigration}, {30, serviceEventMigration},
		{31, decisionCancellationMigration}, {32, decisionResponseMigration},
		{33, auditMigration}, {34, initiativeBacklogMigration},
		{35, initiativePreparationMigration}, {36, initiativeAbandonmentMigration},
		{37, initiativeControlMigration}, {38, backlogPromotionMigration},
		{39, integrationApplicationMigration}, {40, taskMergeMigration},
		{41, taskConsumedContractsMigration},
	}
	for _, migration := range remaining {
		if err := store.applyVersionedMigration(ctx, migration.version, migration.script); err != nil {
			return err
		}
	}
	if err := store.applyVersionedMigration(ctx, 42, reconciledReportOutboxMigration); err != nil {
		return err
	}
	if err := store.applyVersionedMigration(ctx, 43, taskResumeLaunchMigration); err != nil {
		return err
	}
	if err := store.applyVersionedMigration(ctx, 44, initiativeContractArtifactMigration); err != nil {
		return err
	}
	if err := store.applyVersionedMigration(ctx, 45, integrationConflictRecoveryMigration); err != nil {
		return err
	}
	if err := store.applyIntegrationPreparationMigration(ctx); err != nil {
		return err
	}
	return store.backfillReconciledComisReports(ctx)
}

func (store *Store) applyVersionedMigration(ctx context.Context, version int, migration string) error {
	var applied int
	if err := store.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version).Scan(&applied); err != nil {
		return fmt.Errorf("inspect SQLite migration %d: %w", version, err)
	}
	if applied == 1 {
		return nil
	}
	return store.applyMigration(ctx, migration)
}

func (store *Store) applyMigration(ctx context.Context, migration string) error {
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin SQLite migration: %w", err)
	}
	if _, err := transaction.ExecContext(ctx, migration); err != nil {
		_ = transaction.Rollback()
		return fmt.Errorf("apply SQLite migration: %w", err)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit SQLite migration: %w", err)
	}
	return nil
}
