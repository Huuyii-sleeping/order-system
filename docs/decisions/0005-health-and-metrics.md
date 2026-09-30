# 0005：区分存活、就绪和运行指标

## 背景

进程能够响应 HTTP，不代表它仍能访问数据库并处理业务请求。部署平台需要分别
判断是否应该重启实例，以及是否应该继续向实例分发流量；排障人员还需要观察
请求量、错误状态和耗时。

## 决策

- `/healthz` 只检查 HTTP 进程存活，不访问外部依赖。liveness 失败时平台可以
  重启进程，不能因为数据库短暂故障就反复重启正常的应用进程。
- `/readyz` 使用有超时的 `PingContext` 检查当前存储。失败时返回 503，让负载
  均衡器停止向该实例转发流量；底层错误只记录在服务端日志。
- `/metrics` 使用 Prometheus 文本协议暴露 Go Runtime、进程、HTTP 和 Kafka 消费指标。
- HTTP 数量以 Method、路由模板、状态码为标签，耗时以 Method、路由模板为
  标签，同时记录当前请求数。动态 URL 使用 `/orders/{orderID}` 等模板，未知
  地址统一记为 `unmatched`。
- Kafka 指标使用 Consumer Group、Topic、Partition 和固定枚举结果作为标签；消息 ID、
  订单 ID 和错误文本不进入标签。指标覆盖处理尝试、重试原因、DLQ 发布、offset 提交
  和基于最近 Fetch High Watermark 的 Partition Lag。
- Consumer Lag 是当前实例的 Fetch 快照，不是额外向 Broker 查询的集群级实时值。
  Partition 被 Rebalance 收回或丢失时删除对应时间序列，避免陈旧数据误导告警。
- 本地观测栈由 Prometheus 定时拉取 `/metrics`，Grafana 只通过 Prometheus 查询数据，
  不直接连接应用。抓取配置、规则和仪表盘放在 `deployments/local/observability/`，
  与应用代码和部署环境分离。
- 告警表达式和 `for` 持续时间一同版本化，并使用 `promtool test rules` 验证触发与
  不触发场景，避免仅做 YAML 语法检查。
- 本地仪表盘优先展示 RED 信号（请求速率、错误率、耗时）和消息消费状态（处理结果、
  重试、Lag、死信与 Offset 提交失败）。
- 指标注册表由组合根创建并注入 HTTP 层，测试使用独立注册表，避免全局注册器
  重复注册和测试相互污染。

## 边界

当前 `/metrics` 与业务 API 共用端口，生产部署必须由网络策略或反向代理限制
访问。Readiness 检查当前模式所需的 PostgreSQL 和 Kafka；非关键依赖不应随意加入，
否则它的故障会让整个实例退出流量。本地 Prometheus 和 Grafana 配置用于学习和
开发，不包含生产级高可用、长期存储、身份认证，也没有配置 Alertmanager 通知渠道。
