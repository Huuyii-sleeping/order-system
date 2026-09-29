# 0008：使用 Kafka 协议连接 Outbox 与幂等消费者

## 背景

进程内消息适配器可以验证 Outbox、Inbox 和支付状态机，但不能展示跨进程消息的
持久化、分区、消费者组、offset 提交和重平衡语义。

## 决策

- 保留 `direct` 作为默认开发模式，新增显式的 `kafka` 消息驱动。Kafka 模式必须
  使用 PostgreSQL，确保 Inbox 和支付任务能跨进程重启保存。
- 本地 Broker 使用 Redpanda，但应用只使用 Kafka 协议和通用客户端，不调用
  Redpanda 专有 API。
- Kafka Record 的 Topic 和 Value 保存事件名称与 JSON payload；订单 ID 作为 Key，
  同一订单的事件会进入同一分区；消息 ID 和业务发生时间保存在 Header 中。
- 生产者使用同步确认。Broker 返回成功后 Outbox Worker 才更新 `published_at`。
- 消费者关闭自动提交。只有 Inbox、Payment 和 PaymentTask 的数据库事务成功后，
  才同步提交对应 offset。处理或提交失败时回退本地 fetch 位置并重试该记录。
- 每次 Poll 一条消息，并只在这一条业务处理和 offset 提交期间阻止消费者组
  rebalance，避免提交已经失去所有权的分区。处理或提交失败后先回退客户端本地
  fetch 位置，再允许 rebalance；若分区此时转交给其他实例，也只是产生重复处理，
  最终由 Inbox 去重。
- Handler 对同一条记录进行有限次数重试；达到上限后同步写入死信 Topic。死信记录
  保留原始 Key、Value、业务信封以及原 Topic/Partition/Offset、消费者组、尝试次数、
  失败时间和错误原因。只有死信写入成功后才提交原消息 offset。

## 投递语义

Kafka offset 不是业务幂等记录。以下崩溃窗口始终存在：

1. Broker 已确认生产，但 Outbox 还没标记成功，生产者会再次发送。
2. 消费者业务事务已经提交，但 offset 尚未提交，消费者会再次处理。

所以链路仍然是至少一次，而不是恰好一次。重复消息由 PostgreSQL Inbox 消除业务
副作用；相同消息 ID 但内容不同则作为契约冲突处理。

## 当前边界

死信发布与原 offset 提交不能组成跨 Kafka 操作的原子事务。死信已写入但 offset
提交失败时可能再次生成死信，因此重放工具必须用原 Topic/Partition/Offset 去重。
当前只提供隔离和审计数据，没有自动重放；应在修复根因后通过受控工具人工重放。

本节没有实现 Schema Registry、认证加密、多 Broker 容灾和消费者积压/死信告警。
