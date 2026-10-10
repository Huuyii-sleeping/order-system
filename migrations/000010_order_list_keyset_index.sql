CREATE INDEX orders_customer_created_at_id_idx
    ON orders (customer_id, created_at DESC, id DESC);

DROP INDEX orders_customer_created_at_idx;
