package sqlite

const integrationAbortedRecoveryMigration = `
DROP INDEX integration_applications_recovery_idx;
CREATE UNIQUE INDEX integration_applications_recovery_idx
ON integration_applications(recovery_operation_id)
WHERE recovery_operation_id <> '' AND status <> 'aborted';
INSERT INTO schema_migrations(version, applied_at)
VALUES (51, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`
