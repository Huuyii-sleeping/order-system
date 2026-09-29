CREATE TABLE inbox_messages (
    message_id TEXT PRIMARY KEY,
    topic TEXT NOT NULL CHECK (length(trim(topic)) > 0),
    payload_hash TEXT NOT NULL CHECK (length(payload_hash) = 64),
    processed_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE payments (
    id TEXT PRIMARY KEY,
    order_id TEXT NOT NULL REFERENCES orders (id) ON DELETE RESTRICT,
    customer_id TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
    status TEXT NOT NULL CHECK (
        status IN ('pending', 'processing', 'succeeded', 'failed', 'unknown')
    ),
    provider_reference TEXT,
    failure_reason TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL CHECK (updated_at >= created_at),
    CONSTRAINT payments_order_id_unique UNIQUE (order_id),
    CHECK (
        (status = 'succeeded' AND provider_reference IS NOT NULL AND failure_reason IS NULL)
        OR
        (status IN ('failed', 'unknown') AND provider_reference IS NULL AND failure_reason IS NOT NULL)
        OR
        (status IN ('pending', 'processing') AND provider_reference IS NULL AND failure_reason IS NULL)
    )
);

CREATE TABLE payment_tasks (
    id TEXT PRIMARY KEY,
    payment_id TEXT NOT NULL REFERENCES payments (id) ON DELETE CASCADE,
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    next_attempt_at TIMESTAMPTZ NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_id TEXT,
    locked_until TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    last_error TEXT,
    CONSTRAINT payment_tasks_payment_id_unique UNIQUE (payment_id),
    CONSTRAINT payment_tasks_idempotency_key_unique UNIQUE (idempotency_key),
    CHECK ((lease_id IS NULL) = (locked_until IS NULL))
);

CREATE INDEX payment_tasks_pending_idx
    ON payment_tasks (next_attempt_at, id)
    WHERE completed_at IS NULL;
