-- Escalation: a channel can get down alerts only when nobody has
-- acknowledged the outage after this many minutes (0 = right away).
ALTER TABLE notification_channels ADD COLUMN escalate_after_min INT NOT NULL DEFAULT 0
    CHECK (escalate_after_min >= 0 AND escalate_after_min <= 1440);
