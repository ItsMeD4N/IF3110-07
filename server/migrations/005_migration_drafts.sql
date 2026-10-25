CREATE TABLE IF NOT EXISTS platform_migration_drafts (
    id          BIGSERIAL PRIMARY KEY,
    database_id BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    name        TEXT        NOT NULL DEFAULT '',
    sql_text    TEXT        NOT NULL,
    created_by  TEXT        NOT NULL,
    updated_by  TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS platform_migration_drafts_database_idx
    ON platform_migration_drafts (database_id, updated_at DESC);

ALTER TABLE platform_migration_history
    ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT '';
