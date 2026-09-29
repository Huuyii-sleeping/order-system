# Order System

这是一个用于学习 Go 后端工程的模块化单体订单系统。目前包含商品查询、
库存预占、事务下单、幂等创建订单、用户注册登录、短期 Access Token、
Refresh Token 轮换、订单所有权隔离、HTTP 中间件，以及内存和 PostgreSQL
两种存储实现。订单创建事件会可靠地驱动支付消费者和支付任务状态机。

## 直接运行（内存模式）

默认不依赖外部服务：

```bash
go run ./cmd/api
```

服务监听 `:8080`。进程退出后，内存中的订单会丢失。

运行状态端点：

- `GET /healthz`：liveness，只判断 HTTP 进程是否存活。
- `GET /readyz`：readiness，检查当前实例依赖的存储是否可用。
- `GET /metrics`：Prometheus 文本格式的进程与 HTTP 指标。

```bash
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/metrics
```

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

登录并从响应中取得 `access_token`。Refresh Token 会由服务端写入
`HttpOnly` Cookie；同时还会写入前端可读取的 `csrf_token` Cookie：

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

Access Token 过期后，浏览器携带登录时收到的 Cookie 调用刷新接口：

```bash
curl -i -X POST http://127.0.0.1:8080/auth/refresh \
  -H 'Cookie: refresh_token=<refresh-token>; csrf_token=<csrf-token>' \
  -H 'X-CSRF-Token: <csrf-token>'
```

每次刷新都会轮换 Refresh Token。旧 Refresh Token 再次使用会被识别为重放，
并撤销同一个凭证家族中的 Access Session 和 Refresh Token。

登录和刷新接口默认按客户端连接地址进行单实例限流，超限返回
`429 Too Many Requests` 和 `Retry-After`。HTTPS 部署必须将
`SECURE_COOKIES` 设置为 `true`，本地 HTTP 开发保持 `false`。

HTTP 指标按 Method、路由模板和状态码聚合，例如动态订单地址统一记录为
`/orders/{orderID}`，不会把真实订单 ID 写入标签。`/metrics` 应只暴露给
监控系统或受保护的内部网络，不应直接开放到公网。

创建订单时，订单、库存和 `orders.created.v1` Outbox 事件会在同一个事务中
提交。后台 Worker 按批次认领事件并发布；失败后使用指数退避重试。当前使用
进程内消息适配器把事件交给支付消费者，后续可以替换为 Kafka 等消息系统。
Outbox 提供至少一次投递，下游通过 Inbox 消息 ID 去重。

支付消费者在同一个事务中写入 Inbox、创建 `pending` 支付和支付任务。另一个
Worker 在事务外调用模拟支付 Provider，并用稳定的支付 ID 作为第三方幂等键。
明确成功或拒绝会结束任务；网络错误会把支付标记为 `unknown` 并指数退避重试。
模拟 Provider 只用于本地学习，不代表真实扣款。

相同参数和 `Idempotency-Key` 重试时会返回原订单，不会重复扣库存。
同一个用户用同一个 Key 携带不同参数时返回 `409 Conflict`。不同用户的
幂等键空间相互独立。

## 检查

不依赖 PostgreSQL 的默认检查：

```bash
make check
```

项目结构和依赖方向见 `docs/architecture/project-structure.md`。
