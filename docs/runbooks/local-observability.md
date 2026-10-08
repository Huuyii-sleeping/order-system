# 本地可观测性运行手册

## 目标和边界

本手册用于在本机验证以下链路：

```text
Order System /metrics -> Prometheus -> PromQL / alerts -> Grafana
Order System OTLP/HTTP -> Tempo -> TraceQL -> Grafana
```

Prometheus、Tempo 和 Grafana 运行在 Docker 中，Order System 仍由宿主机上的 Go 命令启动。
配置仅供本地学习：端口只绑定到 `127.0.0.1`，Grafana 使用公开的开发密码，
没有配置 Alertmanager、长期存储、高可用或生产认证。

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
2. `promtool test rules` 使用构造的时间序列验证告警何时触发或保持静默。

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
order_system_kafka_consumer_lag_records
sum by (outcome) (rate(order_system_kafka_processing_attempts_total[5m]))
sum by (outcome) (rate(order_system_payment_provider_requests_total[5m]))
order_system_payment_provider_circuit_state
```

在 Grafana 的 `Explore` 中选择 `Tempo` 数据源，使用 TraceQL 查询：

```traceql
{ resource.service.name = "order-system" }
```

打开一条 `POST /orders` Trace 后，应能看到 HTTP、两次 Outbox 派发、两次 Kafka
生产与消费，以及 Payment Task。它们共用一个 TraceID，但每个异步阶段是独立 Span。
请求已经返回后出现的 Span 时间间隔体现真实排队时间；消息重试会出现额外的消费 Span。

## 理解和模拟告警

告警表达式为真后不一定立即进入 `firing`：带有 `for` 的规则会先进入 `pending`，
条件连续满足指定时间才会触发。这能过滤一次抓取失败或短暂流量尖峰。

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
6. 查询有结果但告警未触发：检查表达式、`for` 持续时间和规则评估时间。
7. 告警已触发但无人收到通知：当前没有 Alertmanager，这是预期边界。
8. 支付熔断已打开：按 `outcome` 区分超时、依赖错误和非法响应，并参考
   `payment-provider-resilience.md` 验证冷却后的半开探测。

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
