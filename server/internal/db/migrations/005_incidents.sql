-- Incidents (written by people) and scheduled maintenance windows.
CREATE TABLE incidents (
    id                 BIGSERIAL PRIMARY KEY,
    kind               TEXT        NOT NULL CHECK (kind IN ('incident', 'maintenance')),
    title              TEXT        NOT NULL,
    status             TEXT        NOT NULL CHECK (status IN (
                           'investigating', 'identified', 'monitoring', 'resolved',
                           'scheduled', 'in_progress', 'completed', 'cancelled')),
    impact             TEXT        NOT NULL DEFAULT 'minor' CHECK (impact IN ('none', 'minor', 'major', 'critical')),
    scheduled_start    TIMESTAMPTZ,
    scheduled_end      TIMESTAMPTZ,
    auto_transition    BOOLEAN     NOT NULL DEFAULT true,
    notify_subscribers BOOLEAN     NOT NULL DEFAULT true,
    created_by         BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at        TIMESTAMPTZ,
    CHECK (kind <> 'maintenance' OR (scheduled_start IS NOT NULL AND scheduled_end > scheduled_start))
);
CREATE INDEX incidents_open_idx ON incidents (kind, status) WHERE resolved_at IS NULL;
CREATE INDEX incidents_created_idx ON incidents (created_at DESC);

CREATE TABLE incident_updates (
    id          BIGSERIAL PRIMARY KEY,
    incident_id BIGINT      NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    status      TEXT        NOT NULL,
    body        TEXT        NOT NULL DEFAULT '',
    created_by  BIGINT      REFERENCES users(id) ON DELETE SET NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX incident_updates_incident_idx ON incident_updates (incident_id, created_at DESC);

CREATE TABLE incident_monitors (
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    monitor_id  BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    impact      TEXT   NOT NULL DEFAULT 'degraded' CHECK (impact IN ('degraded', 'down', 'maintenance')),
    PRIMARY KEY (incident_id, monitor_id)
);
CREATE INDEX incident_monitors_monitor_idx ON incident_monitors (monitor_id);

CREATE TABLE incident_pages (
    incident_id BIGINT NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
    page_id     BIGINT NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    PRIMARY KEY (incident_id, page_id)
);
CREATE INDEX incident_pages_page_idx ON incident_pages (page_id);

-- Failed checks during maintenance are counted here, not as downtime.
ALTER TABLE daily_stats ADD COLUMN maintenance INT NOT NULL DEFAULT 0;
