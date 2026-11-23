-- New check types, and push (heartbeat) monitors with their own URL token.
ALTER TABLE monitors DROP CONSTRAINT monitors_type_check;
ALTER TABLE monitors ADD CONSTRAINT monitors_type_check
    CHECK (type IN ('http', 'tcp', 'ping', 'dns', 'ssl', 'json', 'smtp', 'grpc', 'push'));
ALTER TABLE monitors ADD COLUMN push_token TEXT UNIQUE;
