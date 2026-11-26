ALTER TABLE user_groups
    ADD COLUMN IF NOT EXISTS leader_user_id UUID NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'user_groups_leader_member_fk'
    ) THEN
        ALTER TABLE user_groups
            ADD CONSTRAINT user_groups_leader_member_fk
            FOREIGN KEY (id, leader_user_id)
            REFERENCES group_members (group_id, user_id)
            ON DELETE SET NULL (leader_user_id);
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS user_groups_leader_idx
    ON user_groups (leader_user_id) WHERE leader_user_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS platform_approval_requests (
    id                       BIGSERIAL PRIMARY KEY,
    database_id              BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    migration_id             BIGINT      NOT NULL REFERENCES platform_migrations (id) ON DELETE CASCADE,
    migration_revision       INTEGER     NOT NULL CHECK (migration_revision > 0),
    action                   TEXT        NOT NULL
        CHECK (action IN ('up', 'rollback', 'forward', 'batch_up', 'batch_resume', 'batch_revert')),
    target_execution_id      BIGINT      NULL REFERENCES platform_migration_executions (id) ON DELETE SET NULL,
    requested_by             TEXT        NOT NULL,
    requested_by_user_id     UUID        NULL REFERENCES users (id) ON DELETE SET NULL,
    note                     TEXT        NOT NULL DEFAULT '',
    status                   TEXT        NOT NULL DEFAULT 'pending_review'
        CHECK (status IN ('pending_review', 'approved', 'rejected', 'consumed', 'superseded')),
    consumed_idempotency_key TEXT        NULL,
    consumed_job_id          UUID        NULL,
    consumed_at              TIMESTAMPTZ NULL,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS platform_approval_requests_open_uq
    ON platform_approval_requests (migration_id, action)
    WHERE status IN ('pending_review', 'approved');

CREATE INDEX IF NOT EXISTS platform_approval_requests_db_idx
    ON platform_approval_requests (database_id, id DESC);

CREATE UNIQUE INDEX IF NOT EXISTS platform_approval_requests_consumed_key_uq
    ON platform_approval_requests (consumed_idempotency_key)
    WHERE consumed_idempotency_key IS NOT NULL;

CREATE TABLE IF NOT EXISTS platform_approval_decisions (
    id                 BIGSERIAL PRIMARY KEY,
    request_id         BIGINT      NOT NULL REFERENCES platform_approval_requests (id) ON DELETE CASCADE,
    group_id           BIGINT      NULL REFERENCES user_groups (id) ON DELETE SET NULL,
    group_name         TEXT        NOT NULL,
    access_level       SMALLINT    NOT NULL CHECK (access_level IN (1, 2)),
    decision           TEXT        NOT NULL DEFAULT 'pending'
        CHECK (decision IN ('pending', 'approved', 'rejected')),
    decided_by_user_id UUID        NULL REFERENCES users (id) ON DELETE SET NULL,
    decided_by         TEXT        NOT NULL DEFAULT '',
    comment            TEXT        NOT NULL DEFAULT '',
    decided_at         TIMESTAMPTZ NULL,
    UNIQUE (request_id, group_id)
);

CREATE INDEX IF NOT EXISTS platform_approval_decisions_group_idx
    ON platform_approval_decisions (group_id) WHERE decision = 'pending';
