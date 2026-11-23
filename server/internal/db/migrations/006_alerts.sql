-- Alert channels and their delivery log.
CREATE TABLE notification_channels (
    id               BIGSERIAL PRIMARY KEY,
    name             TEXT        NOT NULL,
    type             TEXT        NOT NULL CHECK (type IN ('email', 'discord', 'slack', 'webhook')),
    config_enc       TEXT        NOT NULL,           -- encrypted JSON (see internal/secret)
    events           TEXT[]      NOT NULL DEFAULT '{down,recovered}',
    apply_to_all     BOOLEAN     NOT NULL DEFAULT true,
    remind_every_min INT         NOT NULL DEFAULT 0 CHECK (remind_every_min BETWEEN 0 AND 10080),
    enabled          BOOLEAN     NOT NULL DEFAULT true,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Channels that apply to specific monitors (when apply_to_all is false).
CREATE TABLE monitor_channels (
    monitor_id BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    channel_id BIGINT NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    PRIMARY KEY (monitor_id, channel_id)
);
CREATE INDEX monitor_channels_channel_idx ON monitor_channels (channel_id);

CREATE TABLE alert_deliveries (
    id         BIGSERIAL PRIMARY KEY,
    channel_id BIGINT      REFERENCES notification_channels(id) ON DELETE CASCADE,
    monitor_id BIGINT      REFERENCES monitors(id) ON DELETE SET NULL,
    event      TEXT        NOT NULL,
    title      TEXT        NOT NULL DEFAULT '',
    status     TEXT        NOT NULL CHECK (status IN ('sent', 'failed')),
    error      TEXT        NOT NULL DEFAULT '',
    attempts   INT         NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX alert_deliveries_time_idx ON alert_deliveries (created_at DESC);
