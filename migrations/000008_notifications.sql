CREATE TABLE notifications (
    id TEXT PRIMARY KEY,
    payment_id TEXT NOT NULL,
    order_id TEXT NOT NULL,
    customer_id TEXT NOT NULL,
    recipient_id TEXT NOT NULL CHECK (length(trim(recipient_id)) > 0),
    event_type TEXT NOT NULL CHECK (length(trim(event_type)) > 0),
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    status TEXT NOT NULL CHECK (status IN ('pending', 'processing', 'sent', 'failed', 'unknown')),
    provider_reference TEXT,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL CHECK (updated_at >= created_at),
    CONSTRAINT notifications_order_id_unique UNIQUE (order_id),
    CHECK (
        (status = 'sent' AND provider_reference IS NOT NULL AND failure_reason IS NULL)
        OR
        (status IN ('failed', 'unknown') AND provider_reference IS NULL AND failure_reason IS NOT NULL)
        OR
        (status IN ('pending', 'processing') AND provider_reference IS NULL AND failure_reason IS NULL)
    )
);

CREATE TABLE notification_tasks (
    id TEXT PRIMARY KEY,
    notification_id TEXT NOT NULL REFERENCES notifications (id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_id TEXT,
    locked_until TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    trace_parent TEXT,
    trace_state TEXT,
    CONSTRAINT notification_tasks_notification_id_unique UNIQUE (notification_id),
    CONSTRAINT notification_tasks_idempotency_key_unique UNIQUE (idempotency_key),
    CHECK ((lease_id IS NULL) = (locked_until IS NULL))
);

CREATE INDEX notification_tasks_pending_idx
    ON notification_tasks (next_attempt_at, id)
    WHERE completed_at IS NULL;
