# HTTP API 契约维护手册

## 契约位置

- `api/openapi/spec.json`：OpenAPI 3.1 源文件；
- `api/openapi/spec.go`：嵌入并返回契约文件；
- `tests/contract/http_api_test.go`：路由、字段、安全边界和运行时错误响应检查；
- `internal/transport/httpapi/error_response.go`：稳定错误响应和错误码；
- `internal/transport/httpapi/router.go`：真实 HTTP 路由。

## 本地检查

在仓库根目录执行：

```bash
make contract-check
```

这会同时检查事件契约和 HTTP 契约，不需要 PostgreSQL、Redis 或 Kafka。

应用启动后可以读取实际提供的说明书：

```bash
curl -i http://127.0.0.1:8080/openapi.json
```

响应 `Content-Type` 应为 `application/vnd.oai.openapi+json`。该响应来自编译进二进制的同一份
`spec.json`，不是运行时重新拼装的另一份文档。

## 修改接口的正确顺序

1. 先判断修改是否兼容：路径、方法、认证方式、请求必填字段、响应字段、类型、含义和错误码都属于契约；
2. 修改 Handler、DTO 和对应的单元测试；
3. 同步修改 `api/openapi/spec.json`；
4. 更新 `tests/contract/http_api_test.go` 中固定的操作或字段；
5. 执行 `make contract-check` 和 `make check`；
6. 破坏性修改不能直接覆盖现有含义，应设计新版本和调用方迁移窗口。

不要为了让测试通过就直接更新期望值。契约失败的意义正是要求开发者先回答：“旧客户端是否
还能正常工作？”

## 错误响应约定

所有业务 JSON 错误保持两个字段：

```json
{
  "code": "idempotency_key_conflict",
  "error": "idempotency key conflict"
}
```

- `code`：稳定、小写下划线格式，供客户端逻辑判断和统计；
- `error`：可读文案，可以在不改变含义时优化措辞；
- HTTP 状态码：表达协议层结果，例如 `400`、`401`、`404`、`409`、`429`；
- 内部数据库错误、Provider 原始响应、凭据和堆栈不得进入响应。

客户端应同时看 HTTP 状态码和 `code`。例如同为 `409`，`insufficient_stock` 与
`idempotency_key_conflict` 的后续动作不同。

## 常见失败排查

### documented HTTP operations changed

OpenAPI 路径或方法发生变化，但固定的公开操作集合没有同步。先判断兼容性，再决定更新测试、
恢复旧契约或创建新版本。

### documented operation is not routed

OpenAPI 声明了 Router 没有注册的路径或方法。检查 `internal/transport/httpapi/router.go`，以及
支付、通知、Webhook 等条件配置是否已在契约测试 Router 中启用。

### schema properties 或 required fields changed

公开 JSON 字段发生变化。确认是有意的兼容新增，还是误删、误改类型或泄露内部字段。支付和通知
响应不得加入 `provider_reference`、`failure_reason` 或原始 Provider 数据。

### served OpenAPI document differs

运行时端点与 CI 检查对象不一致。保持 `api/openapi/spec.go` 直接嵌入 `spec.json`，不要再维护
第二份字符串、模板或手工响应。

## 本检查没有证明什么

- 没有证明数据库、缓存、Broker 和第三方 Provider 可用；
- 没有证明完整订单生命周期成功；
- 没有证明性能容量和发布安全；
- 没有验证生产网关、域名或 TLS 配置。

这些分别由集成测试、E2E、性能基线和发布验收承担。
