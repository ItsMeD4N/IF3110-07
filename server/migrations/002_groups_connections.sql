CREATE TABLE IF NOT EXISTS user_groups (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS user_groups_name_lower_uq ON user_groups (lower(name));

CREATE TABLE IF NOT EXISTS group_members (
    group_id BIGINT      NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    user_id  UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX IF NOT EXISTS group_members_user_idx ON group_members (user_id);

CREATE TABLE IF NOT EXISTS db_connections (
    id                 BIGSERIAL PRIMARY KEY,
    name               TEXT        NOT NULL,
    description        TEXT        NOT NULL DEFAULT '',
    host               TEXT        NOT NULL,
    port               INTEGER     NOT NULL CHECK (port BETWEEN 1 AND 65535),
    database_name      TEXT        NOT NULL,
    username           TEXT        NOT NULL,
    password_encrypted BYTEA       NOT NULL,
    ssl_mode           TEXT        NOT NULL DEFAULT 'disable'
        CHECK (ssl_mode IN ('disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS db_connections_name_lower_uq ON db_connections (lower(name));

CREATE TABLE IF NOT EXISTS group_database_grants (
    group_id      BIGINT      NOT NULL REFERENCES user_groups (id) ON DELETE CASCADE,
    connection_id BIGINT      NOT NULL REFERENCES db_connections (id) ON DELETE CASCADE,
    access_level  SMALLINT    NOT NULL CHECK (access_level IN (1, 2)),
    granted_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, connection_id)
);
CREATE INDEX IF NOT EXISTS group_database_grants_conn_idx ON group_database_grants (connection_id);
