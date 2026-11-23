-- Two-factor sign-in: authenticator app (TOTP), recovery codes and passkeys.
ALTER TABLE users ADD COLUMN totp_secret_enc TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN totp_enabled_at TIMESTAMPTZ;

CREATE TABLE recovery_codes (
    id         BIGSERIAL PRIMARY KEY,
    user_id    BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  TEXT        NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX recovery_codes_user_idx ON recovery_codes (user_id);

CREATE TABLE passkeys (
    id            BIGSERIAL PRIMARY KEY,
    user_id       BIGINT      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    credential_id BYTEA       NOT NULL UNIQUE,
    credential    JSONB       NOT NULL,
    name          TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at  TIMESTAMPTZ
);
CREATE INDEX passkeys_user_idx ON passkeys (user_id);
