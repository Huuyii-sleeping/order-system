# Order System

这是一个用于学习 Go 后端工程的模块化单体订单系统。目前包含商品查询、
库存预占、事务下单、幂等创建订单、用户注册登录、短期 Access Token、
Refresh Token 轮换、订单所有权隔离、HTTP 中间件，以及内存和 PostgreSQL
两种存储实现。订单创建事件会可靠地驱动支付消费者和支付任务状态机；消息层
支持默认的进程内模式，以及使用 Redpanda 本地运行的 Kafka 协议模式。Redis 可选地
提供商品详情缓存和多实例共享的认证限流。
通知模块同时支持主动调用 Provider、未知结果查询对账，以及经过 HMAC 签名认证的
Provider Webhook 回调。

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

收到 `SIGINT` 或 `SIGTERM` 后，实例会先让 `/readyz` 返回 `503`，等待负载均衡器摘流，
再停止 HTTP 和后台 Worker。已经进入 Handler 或已经取得租约的工作会在总关闭预算内继续
完成；预算耗尽后才取消在途 Context。配置与演练见
`docs/runbooks/graceful-shutdown.md`。

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
`cmd/migrate` 执行，已执行版本、文件名和 SHA-256 checksum 记录在
`schema_migrations` 表中；已应用迁移发生改名或内容漂移时启动会失败。

PostgreSQL 模式使用 `database/sql` 连接池。默认单实例最多打开 20 条连接、保留 5 条空闲
连接，并分别按 30 分钟总生命周期和 5 分钟空闲时间回收。可以通过
`DATABASE_MAX_OPEN_CONNS`、`DATABASE_MAX_IDLE_CONNS`、
`DATABASE_CONN_MAX_LIFETIME` 和 `DATABASE_CONN_MAX_IDLE_TIME` 调整。连接上限是单实例
预算，多实例理论上限约为“单实例上限 × 实例数”，必须为迁移、运维和其他服务预留数据库连接。
指标、容量计算和饱和排查见
[`docs/runbooks/database-connection-pool.md`](docs/runbooks/database-connection-pool.md)。

## 使用 Redis 缓存和共享限流

启动本地 Redis：

```bash
make cache-up
```

在 PostgreSQL 模式下启用 Redis：

```bash
make run-postgres-redis
```

`GET /products/{productID}` 使用 Cache Aside：缓存未命中时查询 PostgreSQL 并回填；
不存在的商品使用短 TTL 空值缓存；同一进程内的并发 miss 会合并为一次回源。正常缓存
TTL 会加入小幅抖动，避免大量 Key 同时失效。商品列表和创建订单仍直接访问事实仓库，
特别是订单不能使用可能陈旧的缓存价格。

启用 Redis 后，登录与刷新接口的固定窗口限流也由多个应用实例共享。Redis 故障时，
商品详情仍会降级回源数据库；认证限流则返回 `503 Service Unavailable`，避免静默绕过
暴力破解保护。`/readyz` 同时检查 Redis，因为启用后的安全限流依赖它。

运行 Redis 集成测试：

```bash
make test-redis-integration
```

停止 Redis 但保留数据卷：

```bash
make cache-down
```

## 使用 Kafka/Redpanda 消息链路

Kafka 模式需要持久化 Inbox，因此必须和 PostgreSQL 一起使用。启动完整基础设施、
执行数据库迁移并创建三分区的 `orders.created.v1`、`payments.succeeded.v1`、
`payments.failed.v1`、`notifications.requested.v1` Topic、共享的 `orders.created.v1.dlq` 死信 Topic，以及隔离的
集成测试 Topic：

```bash
make infra-up
```

启动使用真实 Broker 的 API：

```bash
make run-kafka
```

要同时启用 Redis，使用 `make run-kafka-redis`。

运行 Kafka 客户端与 Redpanda 的集成测试：

```bash
make test-kafka-integration
```

停止全部基础设施：

```bash
make infra-down
```

也可以把迁移程序和 API 一起构建为非 root 容器，并等待所有依赖与 readiness 就绪：

```bash
make app-up
curl http://127.0.0.1:8080/readyz
make app-down
```

`api` 容器的终止宽限期为 20 秒，覆盖应用默认 10 秒排水预算，并为 Trace 刷新和资源
关闭保留余量。Dockerfile 使用多阶段构建，最终镜像不包含 Go 工具链。

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

按创建时间倒序分页读取自己的订单历史：

```bash
curl 'http://127.0.0.1:8080/orders?limit=20' \
  -H 'Authorization: Bearer <access-token>'

curl 'http://127.0.0.1:8080/orders?limit=20&cursor=<next-cursor>' \
  -H 'Authorization: Bearer <access-token>'
```

列表使用 `(created_at DESC, id DESC)` 复合游标，而不是深分页 `OFFSET`。响应中的
`next_cursor` 为空表示已经到达最后一页；它只是查询位置，不是认证凭据，客户端应当
原样回传而不解析。服务始终从 Access Token 获取用户身份，因此不能用查询参数读取
其他用户的订单。

订单创建后，支付会由消息消费者和后台 Worker 异步处理。使用同一个 Bearer Token
查询当前用户订单对应的支付结果：

```bash
curl http://127.0.0.1:8080/orders/<order-id>/payment \
  -H 'Authorization: Bearer <access-token>'
```

支付终态还会异步创建通知任务。使用同一个 Bearer Token 查询通知状态：

```bash
curl http://127.0.0.1:8080/orders/<order-id>/notification \
  -H 'Authorization: Bearer <access-token>'
```

通知可能经历 `pending -> processing -> sent`，网络错误或超时会进入 `unknown` 并用同一
个通知幂等键退避重试。重新领取 `unknown` 或租约过期的 `processing` 任务时，Worker
会先按幂等键查询 Provider：已发送则直接收敛为 `sent`，查不到才安全重发，查询失败则
继续保持 `unknown`。Provider 明确拒绝才进入 `failed`。通知接口只返回安全状态，不暴露
Provider reference 和原始失败原因。通知任务的故障模拟、数据库排查和指标说明见
[`docs/runbooks/notification-delivery.md`](docs/runbooks/notification-delivery.md)。

支付可能经历 `pending -> processing -> succeeded`；第三方结果不确定时会进入
`unknown` 并重试，明确拒绝则进入 `failed`。订单刚创建、事件尚未被消费时，接口会
暂时返回 `404 payment not found`，客户端可以退避后重试。查询条件同时包含已认证的
用户 ID，其他用户无法通过猜测订单 ID 读取支付信息。响应不暴露 Provider 返回的
内部引用和失败详情，避免把内部诊断信息直接作为公共 API 契约。

支付终态会通过第二条 Transactional Outbox 链路推进订单：支付成功时订单最终从
`pending` 变为 `confirmed`；支付明确失败时订单最终变为 `cancelled`，并在同一个
订单侧事务中释放预占库存。支付结果仍为 `unknown` 时不会取消订单。

Payment Worker 会以固定数量的 goroutine 处理批次；Provider 外层再通过超时、并发舱壁
和 `closed -> open -> half_open` 熔断状态机隔离不稳定的下游。超时、网络错误、舱壁拒绝
和熔断拒绝都只表示结果不确定，因此任务会持久化为 `unknown`，随后用原
`Idempotency-Key` 退避重试；只有 Provider 明确返回业务拒绝时才进入 `failed`。
默认参数和本地故障演练方式见 `docs/runbooks/payment-provider-resilience.md`。

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

过期的 Access Session 和 Refresh Token 不会只依赖查询时的逻辑过滤。服务还会启动
`auth-credential-cleanup` 维护 Worker，按 `AUTH_CREDENTIAL_CLEANUP_INTERVAL` 和
`AUTH_CREDENTIAL_CLEANUP_BATCH_SIZE` 分批物理回收历史凭证；数据库模式依赖已有的
`expires_at` 索引。故障排查和关闭行为见
[`docs/runbooks/auth-credential-cleanup.md`](docs/runbooks/auth-credential-cleanup.md)。

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
商品缓存另有 `order_system_catalog_cache_operations_total`，仅按 `hit`、`negative_hit`、
`miss`、`read_error` 和 `write_error` 等有限结果聚合，不把商品 ID 放入标签。
支付 Provider 指标包括调用次数/耗时、调用前拒绝次数、熔断状态和状态切换次数；
标签只使用固定的结果、拒绝原因和状态，不包含订单 ID、支付 ID 或错误文本。
通知还暴露 `order_system_notification_reconciliations_total`，使用 `found`、`not_found`
和 `error` 三种低基数结果观察 Provider 查询对账是否正常收敛。
认证凭证维护还暴露 `order_system_auth_credential_cleanup_runs_total`、删除行数、批次耗时
和最近成功时间指标；Prometheus 会在清理失败或超过两个小时没有成功批次时告警。
PostgreSQL 模式还暴露连接池的打开、使用中和空闲连接数，以及累计等待次数、等待耗时和连接
回收原因；等待类 Counter 需要使用 `rate()` 或 `increase()` 判断当前压力。

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
Handler 失败、Offset 提交失败、死信发布、数据库连接池持续饱和、支付 Provider 故障、熔断
持续打开，以及通知重试、未知结果和 Provider 对账失败。
阈值是本地学习基线，生产环境应根据
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

执行与 CI 对齐的代码格式、测试、race、vet 以及 Prometheus/Tempo 配置检查：

```bash
make ci
```

仓库中的 CI 还会在代码和观测检查通过后构建 Docker 镜像。CI 只负责验证，不会自动
部署或推送镜像。

在隔离的 PostgreSQL、Redis、Redpanda 和完整 API 容器上执行适配器测试、HTTP 黑盒
订单生命周期，以及依赖中断时的 readiness 恢复演练：

```bash
make integration-check
```

该命令使用独立 Compose Project 和临时数据卷，结束时自动清理；详细范围和端口覆盖
方式见 [`docs/runbooks/integration-and-fault-drills.md`](docs/runbooks/integration-and-fault-drills.md)。
远程 CI 把它放在独立 Integration Job 中，和快速单元测试分层执行。

在隔离 PostgreSQL 中执行真实 `pg_dump`、恢复到新数据库、重新校验迁移历史并比较
业务数据指纹：

```bash
make database-recovery-check
```

项目使用 forward-only 和 Expand/Contract，不把破坏性 down migration 当作普通应用
回滚方式。详细策略见
[`docs/runbooks/database-migration-and-recovery.md`](docs/runbooks/database-migration-and-recovery.md)。

在隔离 PostgreSQL、Redis、Redpanda 和 API 中执行固定并发的 HTTP 性能基线，覆盖商品
详情缓存读取和 liveness：

```bash
make performance-check
```

它输出 RPS、P50/P95/P99、最大延迟和错误率，并检查本地学习阈值。这个结果用于发现
明显性能回归，不代表生产容量；详细参数和边界见
[`docs/runbooks/performance-baseline.md`](docs/runbooks/performance-baseline.md)。

执行本地灰度发布与应用回滚演练：

```bash
make rollout-check
```

它会先启动旧版本，再等待新版本 readiness，通过少量 canary 请求后停止新版本，验证旧版本
仍然能够服务。脚本使用内存模式和独立端口，不连接已有基础设施；真实发布边界见
[`docs/runbooks/rollout-and-rollback.md`](docs/runbooks/rollout-and-rollback.md)。

## 事件契约与版本化

订单、支付和通知模块通过 Outbox 与消息 Topic 传递版本化 JSON 事件。契约测试位于
`tests/contract`，会检查 Topic 名称、公开字段集合、关键字段值和支付事件的敏感字段边界。
兼容的新增字段需要显式评审；删除字段、改变类型或改变语义时，应创建新的 `v2` Topic 和事件类型，
不能直接改变已经被消费者依赖的 `v1`。

运行 HTTP 和事件契约检查：

```bash
make contract-check
```

这项检查不需要 PostgreSQL、Redis、Kafka 或 Kubernetes；它验证静态协议和 HTTP 路由边界，
不替代 Outbox、Inbox、Broker 投递、消费者事务或 HTTP E2E 测试。事件维护步骤见
[`docs/runbooks/event-contracts.md`](docs/runbooks/event-contracts.md)。

## HTTP API 契约与稳定错误码

机器可读的 OpenAPI 3.1 契约位于 `api/openapi/spec.json`。应用会把完全相同的文件嵌入二进制，
并通过以下地址返回，避免仓库说明书和运行时说明书来自两套来源：

```bash
curl http://127.0.0.1:8080/openapi.json
```

契约覆盖业务路由、请求参数、认证方式、成功响应、错误响应以及支付和通知的敏感字段边界。
业务错误同时返回稳定机器码和可读文案：

```json
{
  "code": "payment_not_found",
  "error": "payment not found"
}
```

客户端应根据 HTTP 状态码和 `code` 做逻辑分支，不应解析 `error` 英文文案。新增可选字段仍需
评审；删除字段、重命名、改类型、改含义、改认证方式或改错误码属于破坏性变更，需要版本化或
明确迁移周期。

`make contract-check` 会同时验证 HTTP 和事件契约；HTTP 部分还会把 OpenAPI 中的每个操作交给
真实 Router 探测，防止说明书声明不存在的路由。维护步骤和失败排查见
[`docs/runbooks/http-api-contracts.md`](docs/runbooks/http-api-contracts.md)。

## Kubernetes 部署基线

Kustomize 清单位于 `deployments/kubernetes/base`，包含 API Deployment、ClusterIP Service、
`/healthz` 和 `/readyz` 探针、滚动更新、PodDisruptionBudget、非 root 容器安全边界以及
ConfigMap/Secret 配置分离。它假设 PostgreSQL、Redis 和 Kafka Broker 已由独立基础设施提供，
不在这个 API 清单中伪造生产数据库或消息集群。

只做离线清单渲染：

```bash
make kubernetes-check
```

部署前必须把 `order-system:replace-me` 替换为不可变镜像 tag 或 digest，并在集群中创建
`order-system-runtime` Secret。数据库迁移仍然是独立、可审计的发布步骤，不能依赖 API Pod
启动时自动执行。完整步骤见
[`docs/runbooks/kubernetes-deployment.md`](docs/runbooks/kubernetes-deployment.md)。

项目结构和依赖方向见 `docs/architecture/project-structure.md`。
