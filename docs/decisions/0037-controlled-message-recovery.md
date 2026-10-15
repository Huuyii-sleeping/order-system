# ADR 0037：消息故障使用受控恢复，不直接修改业务表

## 背景

Outbox、Inbox 和 Kafka DLQ 解决的是正常链路中的可靠投递与幂等，但它们不能替代故障后的
运维流程。直接执行 `UPDATE outbox_messages`、删除 Inbox 标记或把 DLQ 内容复制回主 Topic，
可能与正在工作的 Worker 竞争、重复执行业务，甚至重复扣款或通知。

## 决策

1. 提供独立的 `cmd/messageops` 工具，只接受精确消息 ID 或 `Topic/Partition/Offset`，不提供
   无条件批量重放。
2. Outbox 重试必须先检查当前 `attempts`，提交时通过 `expected-attempts` 做乐观并发校验；
   已发布消息和仍持有有效租约的消息不能人工重置。
3. Inbox 只提供只读检查。Inbox 标记与业务效果处于同一事务，删除标记不能撤销业务结果，
   因此不提供 `inbox delete` 或 `inbox replay`。
4. DLQ 重放必须声明允许返回的原 Topic，并保留原 `message_id`、Key、Payload 和业务信封。
   消费者继续依靠 Inbox 幂等。一个 DLQ 坐标只允许进入一次重放流程。
5. Broker 返回错误时记录 `unknown`，不自动再次发送。超时可能发生在 Broker 已写入但确认响应
   丢失之后，必须先查询 Inbox 和业务状态再决定后续动作。
6. 每次写操作都记录操作人、原因、目标、时间和结果；Payload 与原始错误不输出到普通命令结果。

## 影响

- 恢复速度会比直接执行 SQL 慢，但每一步都能解释、复核和审计。
- 当前工具面向单条消息。批量事件应先生成经过评审的清单，再逐条执行或另建有界批处理能力。
- DLQ 重放本身仍是至少一次语义，最终安全性依赖稳定消息 ID 与消费者 Inbox 事务。
