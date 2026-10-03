# 0012：跨持久化异步边界传播 Trace Context

## 背景

一次创建订单会依次经过 HTTP、数据库 Outbox、Kafka、支付 Inbox、Payment Task、
支付结果 Outbox 和订单 Saga。请求返回后原始 `context.Context` 已经结束，Worker 也可能
在另一个进程或服务重启后继续任务，因此仅在函数调用间传递 Context 无法形成完整 Trace。

## 决定

- 使用 OpenTelemetry SDK 创建 Span，通过 OTLP/HTTP 导出到本地 Tempo。
- HTTP 入口使用 W3C `traceparent`/`tracestate`，Span 名称使用路由模板而不是真实 ID。
- Outbox Message 和 Payment Task 持久化 W3C Trace Context；Worker 认领后恢复父级并创建
  新 Span。数据库只保存传播元数据，不保存进程内 `context.Context`。
- Kafka Producer 把传播字段写入标准 Header；Consumer 恢复远端父级后创建 Consumer Span。
- 后台重试创建新的兄弟 Span，不复用已经结束的 Span；至少一次投递可能产生多个消费 Span，
  这是系统真实执行历史，不做隐藏。
- 未配置 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` 时不采样、不发起导出请求，业务可以独立运行。

## 原因

Trace Context 与业务数据一起进入可靠存储，才能跨进程重启和异步等待保持因果关系。
标准 W3C Header 让未来拆分服务或更换 Broker 时不依赖项目私有协议。每个异步阶段保留
独立 Span，可以看清排队时间、重试和失败位置；把所有工作伪装成一个长时间运行的同步 Span
会掩盖真实系统行为。

## 影响与边界

- `outbox_messages` 和 `payment_tasks` 新增可空的 Trace Context 字段，旧记录仍可处理，但会从
  当前阶段开始新 Trace。
- Trace 是诊断信号，不参与业务正确性判断；Tempo 不可用不会回滚订单事务。SDK 在内存中
  批量导出，进程关闭时有界刷新。
- 当前示例全量采样仅适合本地学习。生产环境需要按成本和 SLO 设计采样、保留时间、认证、
  TLS、敏感属性治理和 Collector 高可用。
- 当前没有自动数据库 Client Span，也没有日志后端；日志只补充 `trace_id` 便于人工关联。

## 重新评估条件

拆分为多进程服务、接入真实支付 HTTP API、引入 OpenTelemetry Collector，或 Trace 量达到
必须做尾部采样时，重新评估导出拓扑和采样策略，但继续保留 W3C 传播契约。
