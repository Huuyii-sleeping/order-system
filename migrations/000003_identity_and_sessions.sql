CREATE TABLE users (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL CHECK (email = lower(trim(email)) AND length(email) <= 254),
    password_hash TEXT NOT NULL CHECK (length(password_hash) > 0),
    created_at TIMESTAMPTZ NOT NULL
);

ALTER TABLE users
    ADD CONSTRAINT users_email_unique UNIQUE (email);

CREATE TABLE sessions (
    token_hash TEXT PRIMARY KEY CHECK (length(token_hash) = 64),
    user_id TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL CHECK (expires_at > created_at)
);

CREATE INDEX sessions_user_id_idx ON sessions (user_id);
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

-- 旧版本允许匿名 customer_id，已有学习数据可能没有对应用户。
-- NOT VALID 会保护迁移后的新订单，同时保留旧订单供迁移演示。
ALTER TABLE orders
    ADD CONSTRAINT orders_customer_id_fkey
    FOREIGN KEY (customer_id) REFERENCES users (id) NOT VALID;

-- 幂等键属于某个用户的请求空间，不应该阻止另一个用户使用相同的键。
ALTER TABLE orders DROP CONSTRAINT orders_idempotency_key_unique;
ALTER TABLE orders
    ADD CONSTRAINT orders_customer_idempotency_key_unique
    UNIQUE (customer_id, idempotency_key);
