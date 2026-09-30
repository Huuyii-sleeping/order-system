# 0009：有限重试后隔离 Kafka 毒消息

## 背景

Kafka 分区按 offset 顺序消费。一条格式错误、契约不兼容或持续触发业务错误的消息
如果无限重试，会让同一分区后续的正常消息永久积压。这类无法靠短暂等待恢复的记录
称为毒消息。

## 决策

- 每次从 Kafka 取得一条记录后，Handler 默认最多执行三次，次数可由
  `KAFKA_CONSUMER_MAX_ATTEMPTS` 调整；两次尝试之间使用 `KAFKA_CONSUMER_RETRY`。
- 达到上限后，将原始记录同步发布到 `KAFKA_DEAD_LETTER_TOPIC`。默认 Topic 为
  `orders.created.v1.dlq`。当前订单创建和支付结果事件共用该死信 Topic；生产环境可按
  运维边界拆分消费者和死信 Topic。
- 死信保留原始 Key、Value 和非死信 Header，并追加原 Topic、Partition、Offset、
  Consumer Group、尝试次数、失败时间和截断后的错误原因。
- 只有 Broker 确认死信写入后才提交原记录 offset；死信发布失败则回退本地读取位置，
  不允许跳过原消息。
- 不自动消费或重放 DLQ。修复生产者、事件契约或消费者后，由受控工具审核和重放。

## 结果与边界

毒消息不再永久阻塞分区，且排障人员能定位原记录和失败原因。代价是系统明确放弃
“无限等待这条消息自动恢复”，需要告警和人工处置。

死信发布成功与原 offset 提交成功之间仍有崩溃窗口。如果 offset 提交失败，原消息会
再次处理并可能产生重复死信。因此 DLQ 仍是至少一次语义，重放方必须用
`原 Topic + 原 Partition + 原 Offset` 作为稳定身份进行幂等处理。当前实现也没有按错误
类型区分瞬时错误和永久错误；后续可在业务确有需要时增加错误分类和不同重试策略。
`KAFKA_CONSUMER_MAX_ATTEMPTS` 只限制单轮获取后的 Handler 调用次数；DLQ 发布失败、
offset 提交失败或实例重启时，同一条记录仍可能进入新一轮处理。
