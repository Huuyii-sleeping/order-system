# 0001：使用 PostgreSQL 事务、行锁和幂等唯一约束

状态：已采用

## 背景

进程内 `sync.Mutex` 只能协调同一 Go 进程中的请求。部署多个 API 实例后，
不同实例的锁互不相通，无法共同保护数据库库存。下单还必须保证库存更新
和订单插入一起成功或一起失败，并防止网络重试重复创建订单。

## 决策

- 库存读取使用 `SELECT ... FOR UPDATE` 锁定目标商品行，锁保持到事务结束。
- 库存更新和订单插入处于同一个 PostgreSQL 事务中。
- 同一用户的相同幂等键先取得事务级 advisory lock，使并发重试按顺序检查已有订单。
- `(orders.customer_id, orders.idempotency_key)` 具有联合唯一约束，作为数据库最终一致性防线。
- 数据库约束继续校验正数金额、非负库存和合法订单状态。
- 应用层仍依赖 `Store` 和 `Transaction` 接口，不依赖 PostgreSQL 类型。

## 结果与限制

多个 API 实例共享同一 PostgreSQL 时，同一商品的库存修改不会基于旧值
并发成功，同一用户的相同幂等键也只会产生一个订单；不同用户可各自使用相同
幂等键。不同商品的库存行可以并发处理。

当前一个订单只有一个商品；后续支持多商品订单时，必须按稳定顺序锁定所有
库存行，避免两个事务以不同顺序取得行锁导致死锁。数据库迁移明确采用
forward-only 和 Expand/Contract，应用与数据恢复边界见
[`0024-forward-migrations-and-recovery.md`](0024-forward-migrations-and-recovery.md)。
