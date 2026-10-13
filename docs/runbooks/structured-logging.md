# 结构化日志运行手册

## 目标和边界

应用和迁移进程将一行 JSON 写到 stdout。日志用于解释具体事件，并通过 `request_id` 和
`trace_id` 与 HTTP 响应、Prometheus 指标和 Tempo Trace 关联。

当前本地环境没有集中式日志后端。Prometheus 不保存日志，Tempo 也只保存 Trace；本机直接读取
进程输出或使用 `docker compose logs api`。

## 日志级别

默认配置：

```text
LOG_LEVEL=info
```

允许值及用途：

- `debug`：临时诊断细节，生产环境谨慎开启；
- `info`：正常生命周期和关键成功事件；
- `warn`：能够继续运行，但需要关注的重试、降级或不确定结果；
- `error`：本次操作失败、后台组件退出或进程无法继续启动。

配置表示“最低输出级别”。例如 `warn` 会保留 Warn 和 Error，过滤 Debug 和 Info。非法值会让
进程在监听端口前失败，避免误以为日志配置已经生效。

## 日志形状

一条 HTTP Access Log 类似：

```json
{
  "time": "2030-01-01T00:00:00Z",
  "level": "INFO",
  "msg": "http request",
  "service": "order-system",
  "request_id": "5f3e2d1c0b9a887766554433221100ff",
  "method": "GET",
  "route": "/orders/{orderID}",
  "status": 200,
  "bytes": 128,
  "duration": 12000000,
  "trace_id": "00112233445566778899aabbccddeeff"
}
```

示例值均为虚构值。`duration` 由 Go `slog` JSON Handler 以纳秒数输出。Access Log 使用路由
模板，不把真实订单 ID、查询参数或请求体写入日志。

本地格式化查看：

```bash
go run ./cmd/api | jq
```

容器模式查看：

```bash
docker compose logs --tail=100 api | jq -R 'fromjson? // .'
```

## 按 Request ID 排查

1. 从客户端响应 Header 读取 `X-Request-ID`；
2. 在日志中查找相同的 `request_id`；
3. 先看 Access Log 的路由、状态码和耗时；
4. 再看同一 Request Context 中的业务错误或 Recovery 日志；
5. 如果日志带有 `trace_id`，到 Tempo 查询完整同步与异步调用链。

调用方传入的 `X-Request-ID` 会被校验。包含空格、换行、非 ASCII 字符或超过 128 字节的值不会
进入日志，而是被新的随机 ID 替换。Request ID 只用于关联，不能作为用户身份或幂等键。

## 敏感字段保护

统一 Handler 会对明确命名的敏感属性脱敏：

```go
logger.InfoContext(ctx, "provider call", "access_token", token)
```

输出中的值会变成：

```json
{"msg":"provider call","access_token":"[REDACTED]"}
```

这只是误用兜底，不应把它当成允许记录凭据的理由。以下内容不能写入日志：

- Authorization、Cookie、Access/Refresh Token、Session、密码和签名密钥；
- 原始 HTTP 请求体和响应体；
- Webhook、Kafka 或 Provider 的完整原始报文；
- 数据库连接串、Token Hash、真实个人信息；
- 可能包含上述内容的错误字符串。

优先记录稳定事件名、固定状态、错误类别、耗时和数量。需要定位业务对象时，只记录经过数据分类
评审的最小标识。

## 本地检查

```bash
go test ./internal/platform/logging ./internal/config ./internal/transport/httpapi
```

这些测试验证：

- 日志级别过滤和固定 `service` 字段；
- Context 中的 `request_id`、`trace_id` 自动关联；
- 顶层、嵌套和 `logger.With` 敏感字段脱敏；
- 不安全 Request ID 被替换；
- Access Log 使用路由模板而不是原始资源路径；
- Recovery 不输出 panic 原始值。

## 常见问题

### 没有 Info 日志

检查 `LOG_LEVEL` 是否为 `warn` 或 `error`。级别过滤发生在写日志前，不代表业务代码没有执行。

### 有 Request ID，但没有 Trace ID

Request ID 在 HTTP 入口始终生成；Trace ID 只有在 Context 中存在有效 Span 时才会出现。检查日志
调用是否使用 `InfoContext` 等 Context 方法，以及异步边界是否正确恢复 W3C Trace Context。

### 日志中出现 `[REDACTED]`

说明属性名称触发了敏感字段规则。先确认该字段本来就不应该记录；如果它只是安全的聚合数量，使用
不包含敏感值含义的准确名称，例如 `refresh_tokens_deleted` 已被规则特意保留为数字。

### 仍然发现敏感内容

立即停止继续传播该日志，确认日志后端的访问范围和保留副本，轮换已暴露凭据，然后修复调用点。
字段级脱敏无法识别日志文案、普通错误字符串或调用栈中的秘密，不能只调整属性名称后继续记录。
