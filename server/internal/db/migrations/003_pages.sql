-- Multiple status pages. A page is its own arrangement of sections, and the
-- same monitor may appear on many pages. Global groups become sections of a
-- default page, then go away.

CREATE TABLE status_pages (
    id            BIGSERIAL PRIMARY KEY,
    slug          TEXT        NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    name          TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    logo_url      TEXT        NOT NULL DEFAULT '',
    website_url   TEXT        NOT NULL DEFAULT '',
    accent_color  TEXT        NOT NULL DEFAULT '#2563eb',
    footer_text   TEXT        NOT NULL DEFAULT '',
    show_latency  BOOLEAN     NOT NULL DEFAULT true,
    custom_domain TEXT        UNIQUE,
    is_default    BOOLEAN     NOT NULL DEFAULT false,
    position      INT         NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX status_pages_one_default ON status_pages (is_default) WHERE is_default;

CREATE TABLE page_sections (
    id              BIGSERIAL PRIMARY KEY,
    page_id         BIGINT  NOT NULL REFERENCES status_pages(id) ON DELETE CASCADE,
    name            TEXT    NOT NULL,
    description     TEXT    NOT NULL DEFAULT '',
    position        INT     NOT NULL DEFAULT 0,
    collapsed       BOOLEAN NOT NULL DEFAULT false,
    legacy_group_id BIGINT
);
CREATE INDEX page_sections_page_idx ON page_sections (page_id, position);

CREATE TABLE page_monitors (
    section_id   BIGINT NOT NULL REFERENCES page_sections(id) ON DELETE CASCADE,
    monitor_id   BIGINT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    position     INT    NOT NULL DEFAULT 0,
    display_name TEXT,
    PRIMARY KEY (section_id, monitor_id)
);
CREATE INDEX page_monitors_monitor_idx ON page_monitors (monitor_id);

-- 1. Default page, branded from the old site settings.
INSERT INTO status_pages (slug, name, description, logo_url, website_url, accent_color, footer_text, show_latency, is_default)
SELECT 'main',
       COALESCE(NULLIF(s.value->>'site_name', ''), 'Drix Status'),
       COALESCE(s.value->>'description', 'Live status of our services'),
       COALESCE(s.value->>'logo_url', ''),
       COALESCE(s.value->>'website_url', ''),
       COALESCE(NULLIF(s.value->>'accent_color', ''), '#2563eb'),
       COALESCE(s.value->>'footer_text', ''),
       COALESCE((s.value->>'show_latency')::boolean, true),
       true
FROM (SELECT 1) AS one
LEFT JOIN settings s ON s.key = 'site';

-- 2. Each public group becomes a section with its public monitors.
INSERT INTO page_sections (page_id, name, description, position, collapsed, legacy_group_id)
SELECT p.id, g.name, g.description, g.position, g.collapsed, g.id
FROM groups g CROSS JOIN status_pages p
WHERE p.is_default AND g.is_public;

INSERT INTO page_monitors (section_id, monitor_id, position)
SELECT s.id, m.id, m.position
FROM monitors m JOIN page_sections s ON s.legacy_group_id = m.group_id
WHERE m.is_public;

-- 3. Public monitors without a group go into one trailing section.
INSERT INTO page_sections (page_id, name, position, legacy_group_id)
SELECT p.id,
       CASE WHEN EXISTS (SELECT 1 FROM page_sections) THEN 'Other services' ELSE 'Services' END,
       100000, -1
FROM status_pages p
WHERE p.is_default
  AND EXISTS (SELECT 1 FROM monitors m WHERE m.is_public AND m.group_id IS NULL);

INSERT INTO page_monitors (section_id, monitor_id, position)
SELECT s.id, m.id, m.position
FROM monitors m JOIN page_sections s ON s.legacy_group_id = -1
WHERE m.is_public AND m.group_id IS NULL;

-- 4. Normalise positions and drop the old model.
UPDATE page_sections ps SET position = o.rn
FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY page_id ORDER BY position, id) - 1 AS rn FROM page_sections) o
WHERE ps.id = o.id;

ALTER TABLE page_sections DROP COLUMN legacy_group_id;
ALTER TABLE monitors DROP COLUMN group_id;
ALTER TABLE monitors DROP COLUMN is_public;
DROP TABLE groups;

-- Branding moved to pages; keep only monitoring defaults in settings.
UPDATE settings SET value = value - 'site_name' - 'description' - 'logo_url' - 'website_url'
                                  - 'accent_color' - 'footer_text' - 'show_latency'
WHERE key = 'site';
