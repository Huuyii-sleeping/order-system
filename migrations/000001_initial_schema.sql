CREATE TABLE products (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (length(trim(name)) > 0),
    price_cents BIGINT NOT NULL CHECK (price_cents > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE stocks (
    product_id TEXT PRIMARY KEY REFERENCES products (id) ON DELETE RESTRICT,
    available INTEGER NOT NULL CHECK (available >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE orders (
    id TEXT PRIMARY KEY,
    customer_id TEXT NOT NULL CHECK (length(trim(customer_id)) > 0),
    product_id TEXT NOT NULL REFERENCES products (id) ON DELETE RESTRICT,
    quantity INTEGER NOT NULL CHECK (quantity > 0),
    unit_price_cents BIGINT NOT NULL CHECK (unit_price_cents > 0),
    total_cents BIGINT NOT NULL CHECK (
        total_cents = unit_price_cents::NUMERIC * quantity
    ),
    status TEXT NOT NULL CHECK (status IN ('pending', 'confirmed', 'cancelled')),
    idempotency_key TEXT NOT NULL CHECK (length(trim(idempotency_key)) > 0),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX orders_customer_created_at_idx
    ON orders (customer_id, created_at DESC);

CREATE INDEX orders_product_id_idx
    ON orders (product_id);

ALTER TABLE orders
    ADD CONSTRAINT orders_idempotency_key_unique UNIQUE (idempotency_key);
