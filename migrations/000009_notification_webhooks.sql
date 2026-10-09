CREATE TABLE notification_webhook_events (
    event_id TEXT PRIMARY KEY CHECK (length(trim(event_id)) BETWEEN 1 AND 200),
    notification_id TEXT NOT NULL REFERENCES notifications (id) ON DELETE CASCADE,
    payload_hash TEXT NOT NULL CHECK (payload_hash ~ '^[0-9a-f]{64}$'),
    received_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX notification_webhook_events_notification_id_idx
    ON notification_webhook_events (notification_id, processed_at);
