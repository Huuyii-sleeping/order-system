# 0015：进程排水与优雅关闭

## 背景

容器滚动发布和宿主机停止进程时会发送 `SIGTERM`。如果进程立刻退出，正在执行的 HTTP
请求会断开，已经认领的 Outbox、Payment Task 或 Kafka Record 也可能只完成一半；如果只
关闭 HTTP Server，却继续让后台 Worker 领取新任务，进程又可能永远无法退出。原有实现还会
忽略消息 Consumer 的意外退出，使实例看起来健康但异步链路已经停止。

## 决定

- 收到 `SIGINT`、`SIGTERM`、HTTP Server 异常或后台组件异常时，进入统一关闭流程。
- 先把实例标记为 draining，使 `/readyz` 返回 `503`，但 `/healthz` 仍返回 `200`。
- 保留一段 `SHUTDOWN_DRAIN_DELAY`，让外部负载均衡器观察到 readiness 变化并停止转发新流量。
- 排水延迟结束后调用 `http.Server.Shutdown`，停止接收新连接并等待在途 HTTP 请求完成。
- HTTP 停止后，Outbox Worker、Payment Worker 和 Kafka Consumer 停止领取新工作，但已认领
  的批次或消息继续执行。Kafka Handler 成功后仍提交 offset。
- HTTP 与后台任务排水共享 `SHUTDOWN_TIMEOUT` 总预算。预算耗尽后取消在途 Context，并
  强制关闭 HTTP 连接；依靠数据库租约、Inbox 和稳定幂等键在其他实例或重启后安全重试。
- 后台任务的错误、意外正常返回和 panic 都会上报给主进程并触发非零退出，交给调度器重启。
- 第一次信号触发优雅关闭后恢复默认信号处理，第二次信号可以立即终止卡住的进程。

## 为什么需要两个 Context

一个 Context 无法同时表达“不要再领取新任务”和“把正在执行的任务也取消”。本节为后台
组件分别传入 `stopCtx` 与 `workCtx`：前者控制 Poll/Claim 循环，后者传给已经开始的数据库、
Broker 和 Provider 调用。正常关闭只先取消前者；超过总预算才取消后者。

## 影响与边界

- `SHUTDOWN_DRAIN_DELAY` 必须小于 `SHUTDOWN_TIMEOUT`，并且容器平台给予进程的终止宽限期
  必须在应用排水预算之外为 Trace 刷新和资源关闭留出余量，否则进程会被 `SIGKILL`。
- readiness 传播不是瞬时的。生产环境需要让排水延迟大于探针周期、失败阈值和负载均衡器
  传播延迟；本地默认值只用于学习和演练。
- 本节保证的是“尽力完成在途工作”，不是 exactly-once。强制终止、offset 提交失败或网络
  响应丢失仍可能造成重复投递，最终仍依赖 Inbox、租约和幂等键。
- 当前进程同时承载 HTTP 与后台 Worker，关闭顺序因此必须统一编排。未来拆成独立 Deployment
  后，每种进程应只管理自己的入口和后台组件。
