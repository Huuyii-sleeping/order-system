# 本地可观测性运行手册

## 目标和边界

本手册用于在本机验证以下链路：

```text
Order System /metrics -> Prometheus -> Recording Rules / alerts -> Grafana
Order System OTLP/HTTP -> Tempo -> TraceQL -> Grafana
Order System JSON stdout -> local terminal / docker compose logs
```

Prometheus、Tempo 和 Grafana 运行在 Docker 中，Order System 仍由宿主机上的 Go 命令启动。
配置仅供本地学习：端口只绑定到 `127.0.0.1`，Grafana 使用公开的开发密码，
没有配置 Alertmanager、远程长期存储、高可用或生产认证。Prometheus 本地保留 30 天数据，只为
覆盖 28 天学习用 SLO 窗口；它不构成生产 SLA 证据。
本地栈也没有日志数据库；Prometheus 不接收日志，Tempo 只接收 Trace。结构化日志格式、关联字段和
敏感信息边界见 `structured-logging.md`。

## 启动

先启动依赖和观测栈，确保应用产生第一条 Trace 前 Tempo 已经就绪：

```bash
make infra-up
make observability-up
```

在另一个终端启动应用：

```bash
make run-kafka-traced
```

`observability-up` 会先执行两类检查：

1. `promtool check config` 验证 Prometheus 配置和规则语法。
2. `promtool test rules` 使用构造的时间序列验证 SLI Recording Rule、错误预算告警何时触发或
   保持静默，以及其他组件告警。

随后访问：

- Prometheus Targets：<http://127.0.0.1:9090/targets>
- Prometheus Alerts：<http://127.0.0.1:9090/alerts>
- Tempo：<http://127.0.0.1:3200/ready>
- Grafana：<http://127.0.0.1:3000>

Grafana 本地账号和密码均为 `admin`。打开 `Order System` 文件夹中的
`Order System Overview` 仪表盘。

`make run-kafka-traced` 会设置
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:4318/v1/traces`。直接使用
`go run ./cmd/api` 时该配置为空，应用不采样也不导出 Trace。

## 验收

先确认 Prometheus 已成功抓取应用：

```bash
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=up%7Bjob%3D%22order-system%22%7D' | jq
```

结果中的值应为 `1`。再检查规则已被加载：

```bash
curl -fsS http://127.0.0.1:9090/api/v1/rules | jq
```

Grafana 健康检查：

```bash
curl -fsS http://127.0.0.1:3000/api/health | jq
```

Tempo 就绪检查：

```bash
curl -fsS http://127.0.0.1:3200/ready
```

创建订单并等待消息链路完成后，可以在 Prometheus 查询：

```promql
sum by (route, status) (rate(order_system_http_requests_total[1m]))
100 * (1 - order_system:slo_error_ratio:rate1h{slo="http-availability"})
100 * (1 - order_system:slo_error_ratio:rate1h{slo="http-latency"})
order_system:slo_error_ratio:rate5m{slo="http-availability"} / (1 - 0.999)
order_system:slo_error_ratio:rate5m{slo="http-latency"} / (1 - 0.99)
order_system_kafka_consumer_lag_records
sum by (outcome) (rate(order_system_kafka_processing_attempts_total[5m]))
sum by (outcome) (rate(order_system_payment_provider_requests_total[5m]))
order_system_payment_provider_circuit_state
sum by (outcome) (rate(order_system_auth_credential_cleanup_runs_total[15m]))
sum by (kind) (increase(order_system_auth_credential_cleanup_deleted_rows_total[15m]))
time() - order_system_auth_credential_cleanup_last_success_timestamp_seconds
order_system_database_pool_connections
rate(order_system_database_pool_waits_total[5m])
rate(order_system_database_pool_wait_duration_seconds_total[5m])
```

在 Grafana 的 `Explore` 中选择 `Tempo` 数据源，使用 TraceQL 查询：

```traceql
{ resource.service.name = "order-system" }
```

打开一条 `POST /orders` Trace 后，应能看到 HTTP、两次 Outbox 派发、两次 Kafka
生产与消费，以及 Payment Task。它们共用一个 TraceID，但每个异步阶段是独立 Span。
请求已经返回后出现的 Span 时间间隔体现真实排队时间；消息重试会出现额外的消费 Span。

## 理解和模拟告警

普通告警表达式为真后不一定立即进入 `firing`：带有 `for` 的规则会先进入 `pending`，条件连续
满足指定时间才会触发。这能过滤一次抓取失败或短暂流量尖峰。

SLO 燃烧率告警不使用额外 `for`，而是要求 1h+5m 或 6h+30m 的长短窗口同时超过阈值：长窗口
确认用户影响已经持续，短窗口确认故障此刻仍在发生。它们还要求达到最低业务流量，避免本地的
一个失败请求直接触发值班告警。完整公式和处置步骤见 `service-level-objectives.md`。

最安全的本地故障模拟是停止 API，但保持 Prometheus 运行。大约一次抓取后，
`OrderSystemTargetDown` 会进入 `pending`；连续一分钟抓取失败后进入 `firing`。
重新启动 API，下一次成功抓取后告警恢复。

不要为了演示其他告警而随意修改业务数据库。对应表达式的确定性行为已经由
`promtool test rules` 覆盖；需要调整阈值时，先修改规则测试表达预期，再修改规则。

## 排障顺序

1. `/metrics` 无法访问：先检查应用是否启动以及监听地址。
2. Prometheus Target 为 Down：检查 `host.docker.internal:8080` 是否能从容器访问。
3. Prometheus 有数据但 Grafana 无图：检查自动配置的数据源和 Dashboard 是否加载。
4. Tempo 没有 Trace：确认应用配置了 OTLP 地址、`4318` 未被占用，并等待批量导出。
5. Trace 在异步阶段断开：检查 Outbox、Payment Task 和 Kafka Header 中的 Trace Context。
6. 查询有结果但告警未触发：普通告警检查表达式、`for` 和规则评估时间；SLO 告警还要同时检查
   长短窗口、燃烧率与最低流量门槛。
7. 告警已触发但无人收到通知：当前没有 Alertmanager，这是预期边界。
8. 支付熔断已打开：按 `outcome` 区分超时、依赖错误和非法响应，并参考
   `payment-provider-resilience.md` 验证冷却后的半开探测。
9. 认证凭证清理告警触发：先看 `runs_total{outcome="error"}` 和清理耗时，再检查
   PostgreSQL 连接、锁等待及 `expires_at` 索引；如果最近成功时间持续增长，确认
   `auth-credential-cleanup` 是否仍由后台 Runtime 监管。
10. 数据库连接池饱和：同时检查使用中连接比例与等待速率，再排查慢 SQL、锁等待、长事务和
    实例数变化；不要在没有核对数据库全局连接余量时直接调大单实例上限。完整步骤见
    `database-connection-pool.md`。
11. SLO 面板没有数据：确认业务接口确实有流量、Recording Rule 已加载，并区分“没有样本”和
    “100% 成功”；应用未收到的 DNS、TLS 或入口层失败不会出现在应用 SLI 中。

## 停止

只停止观测组件并保留数据卷：

```bash
make observability-down
```

停止项目的全部 Compose 服务：

```bash
make infra-down
```

两个命令都不会删除命名数据卷。只有明确不再需要本地数据时，才应单独执行
带卷删除选项的 Docker Compose 命令。
