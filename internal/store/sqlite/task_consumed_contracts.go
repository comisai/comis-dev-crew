package sqlite

const taskConsumedContractsMigration = `
ALTER TABLE tasks ADD COLUMN consumed_contracts_json TEXT NOT NULL DEFAULT '[]';
INSERT INTO schema_migrations(version, applied_at)
VALUES (41, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'));
`
