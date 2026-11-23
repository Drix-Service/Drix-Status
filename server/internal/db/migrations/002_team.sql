-- Team accounts: admins become users with roles, plus invites and an audit log.
ALTER TABLE admins RENAME TO users;
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'owner'
    CHECK (role IN ('owner', 'admin', 'editor', 'viewer'));
-- Existing accounts became owners above; new rows must state a role.
ALTER TABLE users ALTER COLUMN role DROP DEFAULT;
ALTER TABLE users ADD COLUMN last_login_at TIMESTAMPTZ;

CREATE TABLE invites (
    id          BIGSERIAL PRIMARY KEY,
    email       TEXT        NOT NULL,
    role        TEXT        NOT NULL CHECK (role IN ('owner', 'admin', 'editor', 'viewer')),
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_by  BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    user_id     BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    user_email  TEXT        NOT NULL DEFAULT '',
    action      TEXT        NOT NULL,
    target_type TEXT        NOT NULL DEFAULT '',
    target_id   TEXT        NOT NULL DEFAULT '',
    summary     TEXT        NOT NULL DEFAULT '',
    ip          TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_time_idx ON audit_log (created_at DESC);
