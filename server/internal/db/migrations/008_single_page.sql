-- Back to a single status page. Everything from any extra pages is merged
-- into the default page, and a unique index keeps it at one row.

-- Make sure exactly one page is the default (the oldest if none is).
INSERT INTO status_pages (slug, name, description, is_default)
SELECT 'main', 'Drix Status', 'Live status of our services', true
WHERE NOT EXISTS (SELECT 1 FROM status_pages);

UPDATE status_pages SET is_default = true
WHERE id = (SELECT id FROM status_pages ORDER BY id LIMIT 1)
  AND NOT EXISTS (SELECT 1 FROM status_pages WHERE is_default);

-- Sections of other pages move to the end of the default page.
UPDATE page_sections s
SET page_id  = d.id,
    position = s.position + 1000
FROM status_pages d
WHERE d.is_default AND s.page_id <> d.id;

-- Subscribers move over (duplicates by email are dropped).
UPDATE subscribers s
SET page_id = d.id
FROM status_pages d
WHERE d.is_default AND s.page_id <> d.id
  AND NOT EXISTS (SELECT 1 FROM subscribers x WHERE x.page_id = d.id AND lower(x.email) = lower(s.email));
DELETE FROM subscribers s USING status_pages d WHERE d.is_default AND s.page_id <> d.id;

-- Incidents shown on other pages are shown on the default page.
INSERT INTO incident_pages (incident_id, page_id)
SELECT DISTINCT ip.incident_id, d.id FROM incident_pages ip, status_pages d
WHERE d.is_default
ON CONFLICT DO NOTHING;
-- Incidents not linked to any page yet are linked too.
INSERT INTO incident_pages (incident_id, page_id)
SELECT i.id, d.id FROM incidents i, status_pages d
WHERE d.is_default
ON CONFLICT DO NOTHING;

DELETE FROM status_pages WHERE NOT is_default;

-- Renumber sections 0..n.
UPDATE page_sections ps SET position = o.rn
FROM (SELECT id, ROW_NUMBER() OVER (ORDER BY position, id) - 1 AS rn FROM page_sections) o
WHERE ps.id = o.id;

UPDATE status_pages SET slug = 'main', custom_domain = NULL;

-- Only ever one page.
CREATE UNIQUE INDEX status_pages_single ON status_pages ((true));
