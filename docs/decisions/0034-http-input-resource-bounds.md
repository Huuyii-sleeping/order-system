# 0034：为 HTTP 输入设置明确的资源边界

## 背景

JSON 解码和 Webhook 原始报文都属于 HTTP 输入。即使业务字段有长度校验，服务也必须先读取
请求体才能完成 JSON 解析；如果没有读取上限，攻击者可以通过超大或分块请求消耗连接、内存和
CPU。请求头也会占用 net/http 的解析内存，超大的 Cookie、Authorization 或自定义 Header
可能在业务路由之前造成资源压力。

项目原本已经使用 `http.MaxBytesReader` 限制 JSON body，但超限错误会和普通 JSON 格式错误
都返回 400，客户端无法区分“请求有语法问题”和“请求超过服务能力边界”，OpenAPI 也没有声明
413 响应。

## 决定

- 所有 JSON 业务入口和通知 Webhook 共享 1 MiB body 上限；通过 `http.MaxBytesReader` 在
  流式读取阶段执行限制，不依赖客户端是否发送 `Content-Length`。
- JSON body 超限统一返回 HTTP 413 和稳定错误码 `request_body_too_large`，不调用任何应用
  UseCase。普通格式错误仍然返回 400 `invalid_request_body`。
- API `http.Server` 显式设置 1 MiB `MaxHeaderBytes`，让请求行和请求头的资源边界在 Router
  之前生效。该类解析级拒绝由标准库处理，不伪装成业务错误。
- Webhook 超限只记录固定的 `too_large` 指标结果，不记录 body 大小、事件 ID 或原始内容，
  保持指标低基数和敏感信息边界。
- OpenAPI 为注册、登录、创建订单和 Webhook 声明 413；客户端按状态码和稳定 `code` 分支，
  不解析错误文案。

## 选择原因

`Content-Length` 检查只能覆盖声明长度，不能单独保护没有长度或恶意分块的请求；
`MaxBytesReader` 会在读取超过上限时返回 `http.MaxBytesError`，适合放在传输层并阻止业务层
看到不完整输入。413 比 400 更准确地表达“请求格式可能正确，但超过资源契约”。

Header 限制无法通过业务 Handler 补救，因为 Header 在进入 Handler 前已经由 HTTP Server 解析；
因此必须在 `http.Server` 上配置，而不是写成 Router 中间件。

## 影响和边界

- 当前上限是代码常量，不按用户、租户或接口动态调整；如果未来出现大文件上传，应设计独立的
  流式上传协议和单独资源预算，而不是提高 JSON 全局上限。
- 413 表示请求没有进入业务 UseCase，不代表客户端可以无限重试；客户端应修正请求或缩小
  payload。
- `MaxHeaderBytes` 超限属于 HTTP 解析层错误，通常由标准库返回 431，业务 OpenAPI 不为它伪造
  每个路由的响应。
- 资源限制不能替代字段长度、认证、限流和超时；这些保护覆盖不同阶段和不同资源。

## 重新评估条件

- 增加文件、批量导入或大批量订单接口时，重新计算 body、连接、CPU 和下游队列预算。
- 真实客户端需要超过 1 MiB 的 JSON 时，先增加专门接口、异步导入和配额，再评估是否改变当前
  契约。
- 生产网关已经有更严格的 Header 或 body 限制时，核对网关和应用的最小共同边界，避免两层
  返回语义不一致。
