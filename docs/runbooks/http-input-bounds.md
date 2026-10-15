# HTTP 输入边界与超大请求排查

## 当前边界

- JSON body：1 MiB，超过后返回 `413` 和 `request_body_too_large`；请求不会调用业务 UseCase。
- Request line 和 Header：API Server 的 `MaxHeaderBytes` 为 1 MiB。超过后请求会在 Router 之前
  被 net/http 拒绝，通常表现为 `431 Request Header Fields Too Large`。
- JSON 解码：只接受一个 JSON 值并拒绝未知字段；语法或字段问题仍返回 `400 invalid_request_body`。
- Webhook：先用同一个 body 上限读取原始报文，再验 HMAC；超限不会进入签名验证和应用处理。

## 验证 JSON body 限制

启动内存模式 API：

```bash
go run ./cmd/api
```

发送一个大于 1 MiB 且 Content-Type 正确的订单请求：

```bash
python3 - <<'PY' | curl -i http://127.0.0.1:8080/api/v1/orders \
  -H 'Authorization: Bearer <valid-access-token>' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: boundary-test' \
  --data-binary @-
import json
print(json.dumps({"product_id": "x" * (1024 * 1024), "quantity": 1}))
PY
```

如果认证中间件先于 body 解码执行，可能先得到 401；使用已认证的测试会话后，预期响应为：

```json
{"code":"request_body_too_large","error":"request body is too large"}
```

不要把真实 Token、Cookie 或 Profile 放进排障命令和日志。

## 区分 400、413 和 431

| 状态 | 含义 | 处理方向 |
| --- | --- | --- |
| 400 | Body 已在边界内，但 JSON 语法、未知字段或多个 JSON 值不合法 | 修正请求格式 |
| 413 | Body 超过 1 MiB JSON 上限 | 缩小 payload 或使用专门的大数据接口 |
| 431 | Request line/Header 超过 HTTP Server 解析上限 | 检查 Cookie、Authorization 和自定义 Header |

## Webhook 排查

Webhook 需要使用原始 body 计算 HMAC。body 超限时：

1. `MaxBytesReader` 在读取阶段返回限制错误；
2. Handler 记录固定的 `too_large` 结果；
3. 返回 413，不验签、不解析 JSON、不调用通知 UseCase。

这样可以避免把超大报文写入日志，也不会让无效输入占用数据库事务或通知任务。

## 常见误区

- 只检查 `Content-Length`：分块传输可以没有这个 Header，必须在读取器上限制。
- 只限制业务字段：JSON 解码器在业务校验前仍可能先读取大量内容。
- 把 413 当作普通 400：客户端无法判断应该修复 JSON 还是缩小请求。
- 把 Header 限制写在 Router：Header 解析发生在 Router 之前，应该配置 `http.Server`。
- 为了解决大文件上传提高 JSON 全局上限：应设计独立上传或异步导入边界。
