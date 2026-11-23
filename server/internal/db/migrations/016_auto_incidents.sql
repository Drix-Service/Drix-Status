-- Automatic status page incidents: marked on the incident, and a monitor can opt out.
ALTER TABLE incidents ADD COLUMN auto BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE monitors ADD COLUMN auto_incident BOOLEAN NOT NULL DEFAULT true;
