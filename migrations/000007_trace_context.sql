ALTER TABLE outbox_messages
    ADD COLUMN trace_parent TEXT,
    ADD COLUMN trace_state TEXT;

ALTER TABLE payment_tasks
    ADD COLUMN trace_parent TEXT,
    ADD COLUMN trace_state TEXT;
