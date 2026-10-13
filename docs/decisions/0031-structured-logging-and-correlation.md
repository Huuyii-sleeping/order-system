# 结构化日志、关联字段与敏感信息边界

## 背景

应用已经有 HTTP Access Log、Worker 错误日志和 OpenTelemetry Trace，但 Logger 在不同进程中
分别创建，`request_id`、`trace_id` 也需要每个调用点手工添加。随着 HTTP、Outbox、Kafka 和
后台任务增多，这会产生三个问题：

- 同一次操作的日志缺少统一关联字段，只能依靠时间和文案猜测；
- 不同实例的日志级别无法统一控制，故障时信息不足，平时又可能产生过多噪声；
- 开发者可能误把 Authorization、Cookie、Token、原始请求体或动态资源路径写入日志。

日志、指标和 Trace 解决的问题不同。指标适合发现趋势和告警，Trace 适合还原调用链，日志适合
记录离散事件和错误上下文。三者可以通过 `trace_id` 关联，但不能互相替代。

## 决定

- API 和迁移进程统一通过 `internal/platform/logging` 创建标准库 `slog` JSON Logger；
- 每条日志固定带 `service`，通过 `LOG_LEVEL=debug|info|warn|error` 控制最低输出级别；
- `InfoContext`、`WarnContext` 和 `ErrorContext` 自动从 Context 补充 `request_id` 和
  `trace_id`，调用方不需要在每个 Worker 中重复拼接；
- HTTP 入口接受调用方的 `X-Request-ID`，但只允许 1 到 128 字节的 ASCII 字母、数字、
  `-`、`_`、`.` 和 `:`；无效值会被替换为服务端生成的随机 ID；
- Access Log 只记录路由模板，例如 `/orders/{orderID}`，不记录原始 URL、查询参数、请求体、
  Authorization 或 Cookie；
- Recovery 只记录 panic 类型和调用栈，不记录可能包含用户输入的 panic 原始值；
- Handler 对明确命名为 password、secret、authorization、cookie、token、api_key、private_key
  等属性统一输出 `[REDACTED]`；嵌套 Group 和 `logger.With(...)` 属性使用相同规则；
- 字段脱敏是最后一道防线，不扫描普通错误字符串，也不授权调用方记录原始凭据、Provider 响应或
  请求体。

## 日志字段边界

| 可以记录 | 不应记录 |
| --- | --- |
| `service`、`component`、固定事件名 | Access Token、Refresh Token、Session、Cookie |
| `request_id`、`trace_id` | Authorization Header、Webhook Secret、API Key |
| HTTP method、路由模板、状态码、耗时、响应大小 | 原始请求体、响应体、Provider 原始报文 |
| 固定枚举状态、批次数量、重试次数 | 用户密码、Token Hash、真实个人信息 |
| 经过评审的内部对象 ID | 动态 ID 组成的日志字段名或无限基数字段 |

对象 ID 是否允许记录要根据数据分类和保留策略决定。本项目的 HTTP Access Log 选择更保守的路由
模板；Worker 为排障保留已有的任务或消息标识，但不记录认证凭据和业务负载。

## 为什么使用 Context 关联

`context.Context` 已经跟随 HTTP 请求、消息消费和 Provider 调用传递。Logger 只在真正写日志时
读取其中的 Request ID 和当前 Span，不需要把 Logger 或多个字符串参数层层加入每一个业务函数。

跨数据库或消息队列时仍然不能持久化 Context 本身。Trace Context 继续使用已有 W3C 字段持久化
和恢复；Request ID 只代表一次 HTTP 请求，不承诺跨越所有异步阶段。

## 影响和边界

- JSON stdout 方便容器平台和日志采集器处理，本地仍可以通过 `go run` 或 `docker compose logs`
  查看；
- 当前本地观测栈没有 Loki 或其他日志后端，Grafana 只查询 Prometheus 和 Tempo；
- 日志级别在进程启动时读取，修改环境变量后需要滚动重启实例；
- Handler 只根据属性名称脱敏，无法可靠识别藏在 `error.Error()`、日志文案或调用栈中的秘密；
- `request_id` 用于关联和客服排障，不是认证凭据，也不能用于幂等或权限判断；
- Debug 日志仍必须遵守同一敏感信息规则，不能因为只在故障期间开启就记录更多凭据。

## 重新评估条件

- 需要跨实例集中检索和保留策略时，引入 Loki、OpenSearch 或云日志服务，并配置访问控制与脱敏；
- 日志成本明显上升时，按事件重要性调整级别、采样和保留时间，而不是删除错误关联字段；
- 进入正式合规环境时，根据数据分类重新审查对象 ID、IP 地址、用户标识和审计日志保留周期；
- 需要运行时动态调级时，引入受认证、可审计的配置渠道，不开放无保护的管理接口。
