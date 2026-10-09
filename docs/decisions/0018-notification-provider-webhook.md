# 0018：用已签名 Webhook 主动收敛通知结果

## 背景

轮询 `Lookup` 能恢复通知 Provider 响应丢失，但它需要等待下一轮任务调度，并持续占用查询配额。真实 Provider 通常还会主动回调最终结果；回调本身可能重复、延迟、乱序或被伪造，不能把一个公网 HTTP 请求直接等同于可信业务结果。

## 决定

- 提供 `POST /webhooks/notifications/provider`，仅在配置 `NOTIFICATION_WEBHOOK_SECRET` 时注册路由。该接口不使用用户 Bearer Token，而使用 Provider 与系统共享的机器凭据。
- Provider 使用 `HMAC-SHA256(secret, timestamp + "." + rawBody)` 生成十六进制签名，分别放入 `X-Webhook-Timestamp` 和 `X-Webhook-Signature`。服务先验证时间窗口和签名，再解析 JSON。
- 回调携带全局唯一 `event_id`、本地 `notification_id`、原始稳定 `idempotency_key` 和结果。事件 ID 与原始请求体 SHA-256 一起持久化：相同事件和内容返回成功但不重复执行；相同事件 ID 对应不同内容返回冲突。
- Webhook 事件记录、Notification 终态和 NotificationTask 完成标记在一个数据库事务中提交。只有事务成功后才返回 200，使 Provider 可以安全地采用至少一次重试。
- 可信回调允许把 `pending`、`processing` 或 `unknown` 收敛为 `sent`/`failed`。已经相同终态时幂等成功；相反终态或成功回调携带不同 Provider reference 时拒绝冲突，不允许终态倒退。
- HTTP 只暴露稳定错误分类，不返回数据库错误或签名细节；指标只使用有限的 outcome 标签，避免事件 ID 造成高基数。

## 选择原因

Webhook 缩短了未知状态持续时间，轮询对账仍作为回调丢失时的兜底。HMAC 证明请求来自持有共享密钥的一方，时间戳窗口缩小截获请求的重放范围，持久化事件 ID 则负责窗口内及服务重启后的业务幂等；三者解决的问题不同，不能相互替代。

数据库行锁把 Webhook 与 Worker 对同一个任务的状态更新串行化。回调先完成任务后，仍持有旧租约的 Worker 会在提交时得到 lease lost，不会覆盖 Webhook 已确认的结果。

## 影响和边界

- Secret 必须通过部署环境或 Secret Manager 注入，至少 32 字节；仓库只保存占位说明。轮换时生产系统通常需要短暂支持新旧两个 Secret，当前学习实现只支持单 Secret。
- 时间戳只限制请求新鲜度，事件 ID 才提供持久化去重；节点时钟需要同步。
- 本实现把 Provider 的 `succeeded`/`rejected` 当作投递终态。若真实渠道还区分 accepted、delivered、read，需要扩展状态机而不是复用现有含义。
- `notification_webhook_events` 会持续增长；生产环境应按审计和 Provider 最大重试期限制定归档或保留策略。
- Webhook 与 `Lookup` 是互补关系。回调可能丢失，因此不能删除第 17 节的对账路径。
