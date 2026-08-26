package sqlite

const initiativeSchedulingMigration = `
CREATE INDEX initiatives_scheduling_idx ON initiatives(state, created_at, handle);
INSERT INTO schema_migrations(version, applied_at)
VALUES (48, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`
