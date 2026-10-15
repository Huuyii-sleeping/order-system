# Outbox、Inbox 与 DLQ 故障恢复

## 先判断消息在哪一层

- Outbox 未发布：业务事务已经提交，但消息仍在数据库等待发送。
- Inbox 已存在：消费者的业务事务已经成功，重复投递会被幂等跳过。
- DLQ 有记录：消费者有限次重试仍失败，原 offset 只有在 DLQ 写入成功后才会提交。

恢复前必须先修复根因并记录工单或故障原因。不要把“重放”当成修复业务代码的替代品。

## 检查与重试 Outbox

```bash
DATABASE_URL='<database-url>' go run ./cmd/messageops outbox-inspect \
  --message-id '<message-id>'
```

确认 `published_at` 为空、租约未生效，并记录输出的 `attempts`。然后执行：

```bash
DATABASE_URL='<database-url>' go run ./cmd/messageops outbox-retry \
  --message-id '<message-id>' \
  --expected-attempts 3 \
  --actor 'operator-id' \
  --reason 'dependency recovered; change reference OPS-123'
```

`expected-attempts` 防止检查后 Worker 已经推进状态，而人工命令仍覆盖新结果。命令只把
`next_attempt_at` 调整为当前时间，真正发布仍由正常 Outbox Worker 完成。

## 检查 Inbox

```bash
DATABASE_URL='<database-url>' go run ./cmd/messageops inbox-inspect \
  --message-id '<message-id>'
```

查到记录表示相同 `message_id + topic + payload_hash` 已在业务事务中处理。工具故意不提供删除：
删除 Inbox 不会回滚订单、支付或通知，却会允许相同消息再次产生业务效果。

## 精确重放 DLQ

先记录 DLQ 的 Topic、Partition、Offset，并确认消费者缺陷已经修复：

```bash
DATABASE_URL='<database-url>' \
KAFKA_BROKERS='127.0.0.1:19092' \
go run ./cmd/messageops dlq-replay \
  --topic 'orders.created.v1.dlq' \
  --partition 0 \
  --offset 42 \
  --allowed-topic 'orders.created.v1' \
  --actor 'operator-id' \
  --reason 'consumer fixed; change reference OPS-124'
```

工具会读取精确坐标、验证 DLQ 元数据和业务信封、移除失败诊断 Header，再把原消息身份送回
声明的主 Topic。`allowed-topic` 防止损坏或伪造的 DLQ Header 把消息发送到任意 Topic。

如果结果为 `unknown`，禁止直接重跑相同命令。先用稳定 `message_id` 检查 Inbox、业务表和
消费者日志；数据库唯一约束也会阻止同一个 DLQ 坐标再次进入重放。

## 禁止操作

- 不删除 Inbox 来“让消息重新消费”。
- 不重置已经发布的 Outbox。
- 不修改有有效租约的记录。
- 不把 DLQ Payload 复制到聊天、工单或普通日志。
- 不在未确认原 Topic、消息 ID 和业务状态时批量重放。
