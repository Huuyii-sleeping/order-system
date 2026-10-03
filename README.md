# Order System

这是一个用于学习 Go 后端工程的模块化单体订单系统。目前包含商品查询、
库存预占、事务下单、幂等创建订单、用户注册登录、短期 Access Token、
Refresh Token 轮换、订单所有权隔离、HTTP 中间件，以及内存和 PostgreSQL
两种存储实现。订单创建事件会可靠地驱动支付消费者和支付任务状态机；消息层
支持默认的进程内模式，以及使用 Redpanda 本地运行的 Kafka 协议模式。

## 直接运行（内存模式）

默认不依赖外部服务：

```bash
go run ./cmd/api
```

服务监听 `:8080`。进程退出后，内存中的订单会丢失。

运行状态端点：

- `GET /healthz`：liveness，只判断 HTTP 进程是否存活。
- `GET /readyz`：readiness，检查当前实例依赖的存储是否可用。
- `GET /metrics`：Prometheus 文本格式的进程、HTTP 与消息指标。

配置 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` 后，应用还会通过 OTLP/HTTP 导出
OpenTelemetry Trace；默认不采样、不导出。

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

## 使用 Kafka/Redpanda 消息链路

Kafka 模式需要持久化 Inbox，因此必须和 PostgreSQL 一起使用。启动完整基础设施、
执行数据库迁移并创建三分区的 `orders.created.v1`、`payments.succeeded.v1`、
`payments.failed.v1` Topic、共享的 `orders.created.v1.dlq` 死信 Topic，以及隔离的
集成测试 Topic：

```bash
make infra-up
```

启动使用真实 Broker 的 API：

```bash
make run-kafka
```

运行 Kafka 客户端与 Redpanda 的集成测试：

```bash
make test-kafka-integration
```

停止全部基础设施：

```bash
make infra-down
```

Redpanda 对外只监听 `127.0.0.1:19092`。它实现 Kafka 协议，用于本地开发时
减少运行成本；应用通过标准 Kafka 客户端接入，不依赖 Redpanda 专有 API。

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

订单创建后，支付会由消息消费者和后台 Worker 异步处理。使用同一个 Bearer Token
查询当前用户订单对应的支付结果：

```bash
curl http://127.0.0.1:8080/orders/<order-id>/payment \
  -H 'Authorization: Bearer <access-token>'
```

支付可能经历 `pending -> processing -> succeeded`；第三方结果不确定时会进入
`unknown` 并重试，明确拒绝则进入 `failed`。订单刚创建、事件尚未被消费时，接口会
暂时返回 `404 payment not found`，客户端可以退避后重试。查询条件同时包含已认证的
用户 ID，其他用户无法通过猜测订单 ID 读取支付信息。响应不暴露 Provider 返回的
内部引用和失败详情，避免把内部诊断信息直接作为公共 API 契约。

支付终态会通过第二条 Transactional Outbox 链路推进订单：支付成功时订单最终从
`pending` 变为 `confirmed`；支付明确失败时订单最终变为 `cancelled`，并在同一个
订单侧事务中释放预占库存。支付结果仍为 `unknown` 时不会取消订单。

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

Kafka 模式还会暴露以下消息指标：

- `order_system_kafka_processing_attempts_total`：Handler 成功和失败次数。
- `order_system_kafka_retries_total`：因 Handler、DLQ 发布或 offset 提交失败安排的重试。
- `order_system_kafka_dead_letters_published_total`：成功写入 DLQ 的记录数。
- `order_system_kafka_offset_commits_total`：同步提交 offset 的成功和失败次数。
- `order_system_kafka_consumer_lag_records`：按 Topic/Partition 记录最近 Fetch 快照的积压。

这些指标只使用 Consumer Group、Topic、Partition 和固定结果标签，不使用订单 ID、
消息 ID 或错误文本，避免产生无法控制的 Prometheus 时间序列。Lag 是当前实例最近一次
Fetch 的 High Watermark 快照；Partition 在 Rebalance 中被收回或丢失时会删除对应序列。

## 使用 Prometheus、Tempo 和 Grafana

应用在宿主机启动后，可以启动本地观测栈：

```bash
make observability-up
```

需要观察完整 Kafka 异步 Trace 时，使用 `make run-kafka-traced` 启动应用；普通
`make run-kafka` 不配置 Trace 导出，因此不依赖 Tempo。

Prometheus 通过 `host.docker.internal:8080/metrics` 每 5 秒抓取应用指标；应用通过
OTLP/HTTP 把 Trace 发给 Tempo。Grafana 启动时自动配置两个数据源并加载指标仪表盘：

- Prometheus：<http://127.0.0.1:9090>
- Prometheus 告警：<http://127.0.0.1:9090/alerts>
- Tempo：<http://127.0.0.1:3200/ready>
- Grafana：<http://127.0.0.1:3000>，本地账号和密码均为 `admin`

只检查 Prometheus 配置和告警规则，不启动长期运行的容器：

```bash
make observability-check
```

停止观测容器但保留本地指标、Trace 和 Grafana 数据：

```bash
make observability-down
```

当前规则覆盖抓取失败、HTTP 5xx 比例、HTTP P95 延迟、Kafka Consumer Lag、
Handler 失败、Offset 提交失败和死信发布。阈值是本地学习基线，生产环境应根据
SLO、正常流量和故障预算调整。完整的启动、验证和故障模拟步骤见
`docs/runbooks/local-observability.md`。

Trace Context 使用 W3C `traceparent`/`tracestate`，并随 Outbox 与 Payment Task 一起
持久化。因此 HTTP 请求结束或进程重启后，异步处理仍能延续同一个 Trace。Kafka Producer
和 Consumer 使用标准 Header 传播；每次重试形成新的 Span，反映至少一次投递的真实执行历史。

创建订单时，订单、库存和 `orders.created.v1` Outbox 事件会在同一个事务中
提交。后台 Worker 按批次认领事件并发布；失败后使用指数退避重试。默认的
`direct` 模式会在进程内调用支付消费者；`kafka` 模式会等待 Broker 确认消息写入，
再标记 Outbox 已发布。消费者只在本地业务事务成功后提交 offset。发布确认和
offset 提交都存在崩溃窗口，因此整个链路仍是至少一次投递，下游通过 Inbox
消息 ID 去重。订单 ID 作为 Kafka Record Key，使同一订单的事件进入同一分区。
消费者每次取得一条消息后默认最多处理三次；仍然失败时，会把原始 Key、Value、业务
信封、原 Topic/Partition/Offset、消费者组、失败时间和错误原因同步写入死信 Topic。
只有 Broker 确认死信写入后才提交原消息 offset，因此毒消息不会永久阻塞后续消息。
死信写入与原 offset 提交不是同一事务，提交失败时可能重新消费并产生重复死信；重放
工具必须使用原 Topic/Partition/Offset 幂等处理，且应先修复根因并经过人工审核。

支付消费者在同一个事务中写入 Inbox、创建 `pending` 支付和支付任务。另一个
Worker 在事务外调用模拟支付 Provider，并用稳定的支付 ID 作为第三方幂等键。
明确成功或拒绝会结束任务；网络错误会把支付标记为 `unknown` 并指数退避重试。
终态、任务完成和支付结果 Outbox 在同一个事务中提交。订单侧通过 Inbox 幂等消费
结果事件，确认订单或执行“取消订单 + 释放库存”的补偿。模拟 Provider 只用于本地
学习，不代表真实扣款。

相同参数和 `Idempotency-Key` 重试时会返回原订单，不会重复扣库存。
同一个用户用同一个 Key 携带不同参数时返回 `409 Conflict`。不同用户的
幂等键空间相互独立。

## 检查

不依赖 PostgreSQL 的默认检查：

```bash
make check
```

项目结构和依赖方向见 `docs/architecture/project-structure.md`。
