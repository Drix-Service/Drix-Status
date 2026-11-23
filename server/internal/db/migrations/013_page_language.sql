-- The status page's language for visitors whose own language isn't
-- available (visitors otherwise get their browser's language).
ALTER TABLE status_pages ADD COLUMN IF NOT EXISTS language TEXT NOT NULL DEFAULT 'en';
