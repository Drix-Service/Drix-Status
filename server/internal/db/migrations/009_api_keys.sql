-- API keys for scripts and CI. Only a SHA-256 hash of the key is stored;
-- the key acts as its creator with its own (equal or lower) role.
CREATE TABLE IF NOT EXISTS api_keys (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT        NOT NULL,
    prefix       TEXT        NOT NULL,
    token_hash   TEXT        NOT NULL UNIQUE,
    role         TEXT        NOT NULL CHECK (role IN ('viewer', 'editor', 'admin')),
    created_by   BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at TIMESTAMPTZ
);

-- Certificate-expiry warnings go wherever "down" alerts already go.
UPDATE notification_channels
SET events = array_append(events, 'cert_expiring')
WHERE 'down' = ANY(events) AND NOT ('cert_expiring' = ANY(events));
