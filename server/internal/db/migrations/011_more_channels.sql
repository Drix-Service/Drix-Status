-- More alert channel types: Telegram, Microsoft Teams, PagerDuty,
-- Opsgenie, ntfy and Pushover.
ALTER TABLE notification_channels DROP CONSTRAINT IF EXISTS notification_channels_type_check;
ALTER TABLE notification_channels ADD CONSTRAINT notification_channels_type_check
    CHECK (type IN ('email', 'discord', 'slack', 'webhook', 'telegram', 'teams', 'pagerduty', 'opsgenie', 'ntfy', 'pushover'));
