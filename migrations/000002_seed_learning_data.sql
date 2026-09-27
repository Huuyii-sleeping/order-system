-- 当前项目仍处于学习阶段，因此用迁移提供一组可立即调用 API 的数据。
-- 正式系统通常会通过商品后台、数据导入任务或独立种子命令创建业务数据。
INSERT INTO products (id, name, price_cents)
VALUES
    ('product-1', 'Mechanical Keyboard', 12900),
    ('product-2', 'Wireless Mouse', 6900)
ON CONFLICT (id) DO NOTHING;

INSERT INTO stocks (product_id, available)
VALUES
    ('product-1', 10),
    ('product-2', 20)
ON CONFLICT (product_id) DO NOTHING;
