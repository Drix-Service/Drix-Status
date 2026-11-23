-- Groups are named sections on the status page (e.g. "EU servers").
CREATE TABLE groups (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    position    INT         NOT NULL DEFAULT 0,
    is_public   BOOLEAN     NOT NULL DEFAULT true,
    collapsed   BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE monitors (
    id          BIGSERIAL PRIMARY KEY,
    group_id    BIGINT      REFERENCES groups(id) ON DELETE SET NULL,
    name        TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    type        TEXT        NOT NULL CHECK (type IN ('http', 'tcp', 'ping', 'dns')),
    target      TEXT        NOT NULL,
    config      JSONB       NOT NULL DEFAULT '{}',
    interval_s  INT         NOT NULL DEFAULT 60 CHECK (interval_s BETWEEN 5 AND 86400),
    timeout_ms  INT         NOT NULL DEFAULT 10000 CHECK (timeout_ms BETWEEN 100 AND 120000),
    retries     INT         NOT NULL DEFAULT 1 CHECK (retries BETWEEN 0 AND 10),
    degraded_ms INT         NOT NULL DEFAULT 0 CHECK (degraded_ms >= 0),
    is_public   BOOLEAN     NOT NULL DEFAULT true,
    enabled     BOOLEAN     NOT NULL DEFAULT true,
    position    INT         NOT NULL DEFAULT 0,
    rev         BIGINT      NOT NULL DEFAULT 1,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX monitors_group_idx ON monitors (group_id, position);

-- Raw check results; trimmed by the retention job.
CREATE TABLE check_results (
    monitor_id BIGINT      NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    checked_at TIMESTAMPTZ NOT NULL,
    status     TEXT        NOT NULL,
    latency_ms REAL        NOT NULL,
    message    TEXT        NOT NULL DEFAULT '',
    agent_id   UUID
);
CREATE INDEX check_results_monitor_time_idx ON check_results (monitor_id, checked_at DESC);
CREATE INDEX check_results_time_brin ON check_results USING BRIN (checked_at);

-- Pre-aggregated per-day counters so uptime bars never scan raw rows.
CREATE TABLE daily_stats (
    monitor_id  BIGINT           NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    day         DATE             NOT NULL,
    total       INT              NOT NULL DEFAULT 0,
    up          INT              NOT NULL DEFAULT 0,
    degraded    INT              NOT NULL DEFAULT 0,
    down        INT              NOT NULL DEFAULT 0,
    latency_sum DOUBLE PRECISION NOT NULL DEFAULT 0,
    PRIMARY KEY (monitor_id, day)
);

-- Status transitions (up -> down, etc.).
CREATE TABLE monitor_events (
    id          BIGSERIAL PRIMARY KEY,
    monitor_id  BIGINT      NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    from_status TEXT        NOT NULL,
    to_status   TEXT        NOT NULL,
    message     TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX monitor_events_time_idx ON monitor_events (created_at DESC);
CREATE INDEX monitor_events_monitor_idx ON monitor_events (monitor_id, created_at DESC);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value JSONB NOT NULL
);

CREATE TABLE admins (
    id            BIGSERIAL PRIMARY KEY,
    email         TEXT        NOT NULL UNIQUE,
    name          TEXT        NOT NULL DEFAULT '',
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Check agents (workers). Only a hash of the token is stored.
CREATE TABLE agents (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name          TEXT        NOT NULL,
    region        TEXT        NOT NULL DEFAULT '',
    token_hash    TEXT        NOT NULL UNIQUE,
    token_prefix  TEXT        NOT NULL,
    disabled      BOOLEAN     NOT NULL DEFAULT false,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ,
    last_hostname TEXT        NOT NULL DEFAULT '',
    last_version  TEXT        NOT NULL DEFAULT ''
);
