package sqlite

const integrationConflictRecoveryMigration = `
ALTER TABLE integration_applications
ADD COLUMN recovery_operation_id TEXT NOT NULL DEFAULT '';
CREATE UNIQUE INDEX integration_applications_recovery_idx
ON integration_applications(recovery_operation_id)
WHERE recovery_operation_id <> '';
INSERT INTO schema_migrations(version, applied_at)
VALUES (45, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`
