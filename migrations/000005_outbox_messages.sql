CREATE TABLE outbox_messages (
    id TEXT PRIMARY KEY,
    topic TEXT NOT NULL CHECK (length(trim(topic)) > 0),
    message_key TEXT NOT NULL CHECK (length(trim(message_key)) > 0),
    payload JSONB NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_id TEXT,
    locked_until TIMESTAMPTZ,
    published_at TIMESTAMPTZ,
    last_error TEXT,
    CHECK ((lease_id IS NULL) = (locked_until IS NULL))
);

CREATE INDEX outbox_messages_pending_idx
    ON outbox_messages (next_attempt_at, occurred_at)
    WHERE published_at IS NULL;
