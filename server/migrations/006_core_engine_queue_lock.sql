CREATE TABLE IF NOT EXISTS platform_migrations (
    id                  BIGSERIAL PRIMARY KEY,
    database_id         BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    version             BIGINT      NOT NULL CHECK (version > 0),
    name                TEXT        NOT NULL DEFAULT '',
    sql_up              TEXT        NOT NULL,
    sql_down            TEXT        NOT NULL DEFAULT '',
    is_irreversible     BOOLEAN     NOT NULL DEFAULT FALSE,
    irreversible_reason TEXT        NOT NULL DEFAULT '',
    execution_mode      TEXT        NOT NULL DEFAULT 'normal'
        CHECK (execution_mode IN ('normal', 'batch')),
    batch_config        JSONB       NULL,
    workflow_status     TEXT        NOT NULL DEFAULT 'draft'
        CHECK (workflow_status IN (
            'draft', 'pending_review', 'approved', 'rejected',
            'queued', 'running', 'applied', 'rolled_back',
            'failed', 'partial', 'partially_reverted', 'uncertain'
        )),
    is_locked_immutable BOOLEAN     NOT NULL DEFAULT FALSE,
    revision            INTEGER     NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_by          TEXT        NOT NULL,
    updated_by          TEXT        NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (database_id, version),
    CHECK (
        (is_irreversible = TRUE AND length(btrim(irreversible_reason)) > 0)
        OR (is_irreversible = FALSE AND length(btrim(sql_down)) > 0)
    )
);

CREATE INDEX IF NOT EXISTS platform_migrations_database_version_idx
    ON platform_migrations (database_id, version DESC);

CREATE OR REPLACE FUNCTION enforce_platform_migration_immutability()
RETURNS TRIGGER AS $$
BEGIN
    IF OLD.is_locked_immutable THEN
        IF NEW.sql_up IS DISTINCT FROM OLD.sql_up
           OR NEW.sql_down IS DISTINCT FROM OLD.sql_down
           OR NEW.is_irreversible IS DISTINCT FROM OLD.is_irreversible
           OR NEW.irreversible_reason IS DISTINCT FROM OLD.irreversible_reason
           OR NEW.execution_mode IS DISTINCT FROM OLD.execution_mode
           OR NEW.batch_config IS DISTINCT FROM OLD.batch_config
           OR NEW.is_locked_immutable = FALSE THEN
            RAISE EXCEPTION 'platform_migrations: definisi SQL migration yang pernah dieksekusi bersifat immutable'
                USING ERRCODE = '22000';
        END IF;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_platform_migrations_immutable ON platform_migrations;
CREATE TRIGGER trg_platform_migrations_immutable
    BEFORE UPDATE ON platform_migrations
    FOR EACH ROW
    EXECUTE FUNCTION enforce_platform_migration_immutability();

CREATE TABLE IF NOT EXISTS platform_migration_executions (
    id                    BIGSERIAL PRIMARY KEY,
    database_id           BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    migration_id          BIGINT      NOT NULL REFERENCES platform_migrations (id) ON DELETE CASCADE,
    migration_version     BIGINT      NOT NULL CHECK (migration_version > 0),
    migration_revision    INTEGER     NOT NULL DEFAULT 1,
    migration_name        TEXT        NOT NULL DEFAULT '',
    action                TEXT        NOT NULL
        CHECK (action IN ('up', 'rollback', 'forward', 'batch_up', 'batch_resume', 'batch_revert')),
    execution_mode        TEXT        NOT NULL DEFAULT 'normal'
        CHECK (execution_mode IN ('normal', 'batch')),
    status                TEXT        NOT NULL
        CHECK (status IN ('pending', 'running', 'success', 'failed', 'partial', 'partially_reverted', 'reverted', 'uncertain')),
    sql_executed          TEXT        NOT NULL,
    actor                 TEXT        NOT NULL,
    reverted_execution_id BIGINT      NULL REFERENCES platform_migration_executions (id) ON DELETE RESTRICT,
    parent_execution_id   BIGINT      NULL REFERENCES platform_migration_executions (id) ON DELETE RESTRICT,
    job_id                UUID        NULL,
    attempt               INTEGER     NOT NULL DEFAULT 1 CHECK (attempt > 0),
    total_batches         INTEGER     NOT NULL DEFAULT 0,
    completed_batches     INTEGER     NOT NULL DEFAULT 0,
    rows_affected         BIGINT      NOT NULL DEFAULT 0,
    reverted_batches      INTEGER     NOT NULL DEFAULT 0,
    reverted_rows         BIGINT      NOT NULL DEFAULT 0,
    last_cursor           TEXT        NULL,
    scope_max_cursor      TEXT        NULL,
    error_message         TEXT        NULL,
    sqlstate              TEXT        NULL,
    correlation_id        TEXT        NOT NULL DEFAULT '',
    trace_id              TEXT        NOT NULL DEFAULT '',
    started_at            TIMESTAMPTZ NULL,
    finished_at           TIMESTAMPTZ NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS platform_migration_executions_db_created_idx
    ON platform_migration_executions (database_id, id DESC);
CREATE INDEX IF NOT EXISTS platform_migration_executions_migration_idx
    ON platform_migration_executions (migration_id, id DESC);
CREATE INDEX IF NOT EXISTS platform_migration_executions_reverted_idx
    ON platform_migration_executions (reverted_execution_id)
    WHERE reverted_execution_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS platform_migration_executions_job_idx
    ON platform_migration_executions (job_id)
    WHERE job_id IS NOT NULL;

CREATE OR REPLACE FUNCTION enforce_execution_history_append_only()
RETURNS TRIGGER AS $$
BEGIN
    IF NEW.database_id IS DISTINCT FROM OLD.database_id
       OR NEW.migration_id IS DISTINCT FROM OLD.migration_id
       OR NEW.migration_version IS DISTINCT FROM OLD.migration_version
       OR NEW.migration_revision IS DISTINCT FROM OLD.migration_revision
       OR NEW.action IS DISTINCT FROM OLD.action
       OR NEW.sql_executed IS DISTINCT FROM OLD.sql_executed
       OR NEW.actor IS DISTINCT FROM OLD.actor
       OR NEW.attempt IS DISTINCT FROM OLD.attempt THEN
        RAISE EXCEPTION 'platform_migration_executions: catatan riwayat migration tidak boleh ditimpa'
            USING ERRCODE = '22000';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_platform_migration_executions_immutable ON platform_migration_executions;
CREATE TRIGGER trg_platform_migration_executions_immutable
    BEFORE UPDATE ON platform_migration_executions
    FOR EACH ROW
    EXECUTE FUNCTION enforce_execution_history_append_only();

CREATE TABLE IF NOT EXISTS platform_migration_batch_checkpoints (
    id                BIGSERIAL PRIMARY KEY,
    execution_id      BIGINT      NOT NULL REFERENCES platform_migration_executions (id) ON DELETE CASCADE,
    root_execution_id BIGINT      NOT NULL REFERENCES platform_migration_executions (id) ON DELETE CASCADE,
    database_id       BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    migration_id      BIGINT      NOT NULL REFERENCES platform_migrations (id) ON DELETE CASCADE,
    batch_number      INTEGER     NOT NULL CHECK (batch_number > 0),
    cursor_start      TEXT        NOT NULL,
    cursor_end        TEXT        NOT NULL,
    rows_affected     BIGINT      NOT NULL DEFAULT 0,
    row_ids           JSONB       NOT NULL DEFAULT '[]'::jsonb,
    recovery_data     JSONB       NOT NULL DEFAULT '[]'::jsonb,
    status            TEXT        NOT NULL
        CHECK (status IN ('committed', 'failed', 'reverted', 'revert_failed')),
    attempt           INTEGER     NOT NULL DEFAULT 1,
    error_message     TEXT        NULL,
    sqlstate          TEXT        NULL,
    duration_ms       BIGINT      NOT NULL DEFAULT 0,
    committed_at      TIMESTAMPTZ NULL,
    reverted_at       TIMESTAMPTZ NULL,
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (root_execution_id, batch_number)
);

CREATE INDEX IF NOT EXISTS platform_migration_batch_checkpoints_root_idx
    ON platform_migration_batch_checkpoints (root_execution_id, batch_number ASC);
CREATE INDEX IF NOT EXISTS platform_migration_batch_checkpoints_exec_idx
    ON platform_migration_batch_checkpoints (execution_id, batch_number ASC);

CREATE TABLE IF NOT EXISTS platform_migration_jobs (
    id              UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    database_id     BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    migration_id    BIGINT      NOT NULL REFERENCES platform_migrations (id) ON DELETE CASCADE,
    execution_id    BIGINT      NULL REFERENCES platform_migration_executions (id) ON DELETE SET NULL,
    action          TEXT        NOT NULL
        CHECK (action IN ('up', 'rollback', 'forward', 'batch_up', 'batch_resume', 'batch_revert')),
    actor           TEXT        NOT NULL,
    idempotency_key TEXT        NOT NULL UNIQUE,
    payload         JSONB       NOT NULL DEFAULT '{}'::jsonb,
    trace_context   JSONB       NOT NULL DEFAULT '{}'::jsonb,
    status          TEXT        NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'retrying', 'completed', 'failed', 'partial', 'partially_reverted', 'uncertain', 'cancelled')),
    attempt         INTEGER     NOT NULL DEFAULT 0,
    max_attempts    INTEGER     NOT NULL DEFAULT 3 CHECK (max_attempts > 0),
    run_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by       TEXT        NULL,
    lease_expires_at TIMESTAMPTZ NULL,
    last_error      TEXT        NULL,
    last_sqlstate   TEXT        NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at      TIMESTAMPTZ NULL,
    completed_at    TIMESTAMPTZ NULL,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS platform_migration_jobs_claimable_idx
    ON platform_migration_jobs (run_at ASC, created_at ASC)
    WHERE status IN ('pending', 'retrying');

CREATE INDEX IF NOT EXISTS platform_migration_jobs_running_lease_idx
    ON platform_migration_jobs (lease_expires_at ASC)
    WHERE status = 'running';

CREATE INDEX IF NOT EXISTS platform_migration_jobs_db_status_idx
    ON platform_migration_jobs (database_id, created_at DESC);

CREATE TABLE IF NOT EXISTS platform_db_locks (
    database_id     BIGINT      PRIMARY KEY REFERENCES db_connections (id) ON DELETE CASCADE,
    lock_token      UUID        NOT NULL,
    fencing_version BIGINT      NOT NULL DEFAULT 1,
    owner_id        TEXT        NOT NULL,
    job_id          UUID        NULL,
    execution_id    BIGINT      NULL,
    state           TEXT        NOT NULL DEFAULT 'held'
        CHECK (state IN ('held', 'blocked_uncertain', 'blocked_partial')),
    reason          TEXT        NOT NULL DEFAULT '',
    acquired_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    heartbeat_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS platform_db_locks_expires_idx
    ON platform_db_locks (expires_at ASC)
    WHERE state = 'held';
