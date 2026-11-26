CREATE TABLE IF NOT EXISTS platform_migration_history (
    id BIGSERIAL PRIMARY KEY,
    database_id BIGINT NOT NULL REFERENCES db_connections(id) ON DELETE CASCADE,
    version BIGINT NOT NULL CHECK (version > 0),
    sql_text TEXT NOT NULL,
    executor TEXT NOT NULL,
    executed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status TEXT NOT NULL CHECK (status IN ('running', 'success', 'failed')),
    error_message TEXT NULL,
    UNIQUE (database_id, version)
);
CREATE INDEX IF NOT EXISTS platform_migration_history_database_version_idx
    ON platform_migration_history (database_id, version DESC);
