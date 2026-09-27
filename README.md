# Order System

这是一个用于学习 Go 后端工程的模块化单体订单系统。目前包含商品查询、
库存预占、事务下单、幂等创建订单、HTTP 中间件，以及内存和 PostgreSQL
两种存储实现。

## 直接运行（内存模式）

默认不依赖外部服务：

```bash
go run ./cmd/api
```

服务监听 `:8080`。进程退出后，内存中的订单会丢失。

## 使用 Docker PostgreSQL

前置条件：安装并启动 Docker Desktop，确保 `docker compose version`
能够正常执行。

启动 PostgreSQL、等待健康检查通过并执行迁移：

```bash
make db-up
```

以 PostgreSQL 模式启动 API：

```bash
make run-postgres
```

运行真实数据库集成测试：

```bash
make test-integration
```

停止数据库容器：

```bash
make db-down
```

`db-down` 不删除数据卷。配置示例见 `.env.example`。数据库迁移由
`cmd/migrate` 执行，已执行版本记录在 `schema_migrations` 表中。

## API 示例

创建订单：

```bash
curl -i http://127.0.0.1:8080/orders \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: learning-request-1' \
  -d '{"customer_id":"customer-1","product_id":"product-1","quantity":2}'
```

使用响应中的订单 ID 查询订单：

```bash
curl http://127.0.0.1:8080/orders/<order-id>
```

相同参数和 `Idempotency-Key` 重试时会返回原订单，不会重复扣库存。
同一个 Key 携带不同参数时返回 `409 Conflict`。

## 检查

不依赖 PostgreSQL 的默认检查：

```bash
make check
```

项目结构和依赖方向见 `docs/architecture/project-structure.md`。
