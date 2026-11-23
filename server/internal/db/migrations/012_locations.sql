-- Check from every location: a monitor can be checked by one worker in
-- each location (the worker's region), and results remember where they
-- came from.
ALTER TABLE monitors ADD COLUMN IF NOT EXISTS multi_location BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE monitors ADD COLUMN IF NOT EXISTS show_locations BOOLEAN NOT NULL DEFAULT true;
ALTER TABLE check_results ADD COLUMN IF NOT EXISTS location TEXT;
