ALTER TABLE users
    ADD COLUMN role TEXT NOT NULL DEFAULT 'customer'
    CHECK (role IN ('customer', 'operator', 'admin'));

CREATE TABLE audit_events (
    id TEXT PRIMARY KEY,
    actor_id TEXT NOT NULL CHECK (length(trim(actor_id)) BETWEEN 1 AND 200),
    action TEXT NOT NULL CHECK (length(trim(action)) BETWEEN 1 AND 200),
    resource_type TEXT NOT NULL CHECK (length(trim(resource_type)) BETWEEN 1 AND 100),
    resource_id TEXT NOT NULL CHECK (length(trim(resource_id)) BETWEEN 1 AND 500),
    outcome TEXT NOT NULL CHECK (outcome IN ('allowed', 'denied')),
    request_id TEXT CHECK (request_id IS NULL OR length(request_id) <= 128),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX audit_events_occurred_at_id_idx
    ON audit_events (occurred_at DESC, id DESC);

CREATE INDEX audit_events_actor_occurred_at_idx
    ON audit_events (actor_id, occurred_at DESC);
