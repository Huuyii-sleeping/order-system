# Order System

这是一个用于学习 Go 后端工程的模块化单体订单系统。目前包含商品查询、
库存预占、事务下单、幂等创建订单、用户注册登录、Bearer 会话认证、
订单所有权隔离、HTTP 中间件，以及内存和 PostgreSQL 两种存储实现。

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

先注册用户：

```bash
curl -i http://127.0.0.1:8080/auth/register \
  -H 'Content-Type: application/json' \
  -d '{"email":"learner@example.com","password":"learning-password"}'
```

登录并从响应中取得 `access_token`：

```bash
curl -i http://127.0.0.1:8080/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"learner@example.com","password":"learning-password"}'
```

使用 Bearer Token 创建和查询自己的订单：

```bash
curl -i http://127.0.0.1:8080/orders \
  -H 'Authorization: Bearer <access-token>' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: learning-request-1' \
  -d '{"product_id":"product-1","quantity":2}'

curl http://127.0.0.1:8080/orders/<order-id> \
  -H 'Authorization: Bearer <access-token>'
```

登出当前设备，或让当前用户的所有设备立即下线：

```bash
curl -i -X POST http://127.0.0.1:8080/auth/logout \
  -H 'Authorization: Bearer <access-token>'

curl -i -X POST http://127.0.0.1:8080/auth/logout-all \
  -H 'Authorization: Bearer <access-token>'
```

相同参数和 `Idempotency-Key` 重试时会返回原订单，不会重复扣库存。
同一个用户用同一个 Key 携带不同参数时返回 `409 Conflict`。不同用户的
幂等键空间相互独立。

## 检查

不依赖 PostgreSQL 的默认检查：

```bash
make check
```

项目结构和依赖方向见 `docs/architecture/project-structure.md`。
