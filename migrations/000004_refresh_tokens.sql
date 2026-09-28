ALTER TABLE sessions
    ADD COLUMN family_id TEXT;

CREATE INDEX sessions_family_id_idx ON sessions (family_id);

CREATE TABLE refresh_tokens (
    token_hash TEXT PRIMARY KEY CHECK (length(token_hash) = 64),
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at),
    used_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at);
