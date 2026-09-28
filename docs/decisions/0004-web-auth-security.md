# 0004：Web 认证的 CSRF、防暴力请求和 Cookie 策略

## 背景

Refresh Token 存在 Cookie 中时，浏览器会自动把它附加到请求。如果只验证
Cookie，恶意网站可能诱导浏览器发出跨站刷新请求。登录和刷新接口也需要限制
单个来源的请求频率，减少暴力尝试和资源消耗。

## 决策

- 刷新接口采用 Double Submit Cookie：登录和刷新成功时生成随机 CSRF Token，
  同时写入 `csrf_token` Cookie；刷新请求必须携带值相同的
  `X-CSRF-Token` Header。
- Refresh Cookie 使用 `HttpOnly`、`SameSite=Strict` 和 `/auth` Path；CSRF
  Cookie 允许页面脚本读取并使用 `/` Path，但它不具备认证能力。
- HTTPS 部署通过 `SECURE_COOKIES=true` 为两个 Cookie 启用 `Secure`。
- 登录和刷新使用独立的固定窗口限流器，按 TCP 连接来源地址计数；不直接信任
  客户端可伪造的 `X-Forwarded-For`。
- 超限响应为 `429 Too Many Requests`，并返回 `Retry-After`。

## 边界

当前限流状态保存在单进程内存中，适用于学习项目和单实例部署。多实例部署时，
各实例无法共享计数，需要换成 Redis 等集中式实现，并由可信反向代理完成真实
客户端地址解析。CSRF 防护只覆盖当前使用 Cookie 的刷新接口；Bearer Token
接口不会由浏览器自动附加凭证，因此不使用该 CSRF 机制。
