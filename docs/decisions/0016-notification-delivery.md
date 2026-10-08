# 0016：使用可靠通知投递链路

## 背景

支付结果已经通过 Outbox 推进订单状态，但用户还需要看到支付成功或失败的通知。通知依赖邮件、短信、站内信或推送等外部 Provider，可能出现网络超时、响应丢失、明确拒绝和重复请求。通知模块也不应该为了查询订单而直接依赖订单数据库。

## 决定

- 订单模块处理支付结果时，把 notifications.requested.v1 写入自己的 Outbox；订单终态更新和通知 Outbox 写入位于同一个订单事务。
- 通知事件携带发送所需的最小快照：notification_id、payment_id、order_id、customer_id、recipient_id、事件类型和金额。通知模块不反向读取 OrderStore。
- 通知消费者使用 Inbox 幂等，并在一个通知事务中同时写入 Inbox、Notification 和 NotificationTask。重复投递不会创建第二条任务。
- NotificationTask 使用 Notification ID 作为稳定幂等键。Provider 网络错误或超时进入 unknown 并指数退避；Provider 明确拒绝进入 failed；成功进入 sent。
- 任务认领使用 PostgreSQL FOR UPDATE SKIP LOCKED 的短事务，提交后通过 lease_id 和 locked_until 保留处理权。Provider 网络调用不持有数据库行锁。
- HTTP 查询只允许已认证用户按 order_id 查询自己的通知，并只返回状态和时间等安全字段，不返回 Provider reference 或原始失败原因。
- 本节使用本地模拟 Provider。真实邮件、短信或推送 SDK 只能通过应用端口接入适配器，不进入领域层。

## 选择原因

通知请求和订单状态共享订单事务，避免出现“订单已经确认但通知事件没有留下”的空档；通知消费端的 Inbox/Task 事务又避免出现“消息已经标记处理但任务没有创建”的空档。Provider 调用无法与 PostgreSQL 组成一个原子事务，因此采用至少一次投递、稳定幂等键和 unknown 状态，允许超时后的安全重试。

## 影响与遗留风险

- 订单确认与通知发送是最终一致的，HTTP 创建订单不会等待通知发送完成，客户端需要查询通知状态或订阅未来的推送事件。
- Provider 必须真正按幂等键去重；如果第三方不支持幂等键，需要额外的查询/对账能力，仍不能用本地数据库事务保证外部发送 exactly-once。
- 当前 recipient_id 使用 customer ID 快照，没有接入真实联系方式和模板系统；后续可以在事件生产边界增加已授权的收件人快照和模板版本。
- 任务和 Kafka 都是至少一次语义，重复消息和租约过期是正常情况，监控应关注通知重试、未知状态和 Provider 错误，而不是假设绝不重复。

## 重新评估条件

- 通知类型超过单一 Provider，需要按渠道拆分任务或增加模板/优先级调度时；
- 通知量需要独立扩缩容，或通知模块需要单独部署时；
- Provider SLA、合规要求或送达回执要求引入状态查询、回执事件和人工对账时。
