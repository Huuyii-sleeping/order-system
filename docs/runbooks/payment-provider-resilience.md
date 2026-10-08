# 支付 Provider 稳定性故障演练

## 目标

验证支付下游超时和连续失败时：HTTP 服务仍可响应、Payment Task 不会丢失、支付保持
`unknown`、熔断器快速拒绝后续调用，并且 Prometheus 能观察到这些行为。这里使用本地
模拟 Provider，不会产生真实扣款。

## 默认配置

```text
PAYMENT_BATCH_SIZE=10
PAYMENT_WORKER_CONCURRENCY=4
PAYMENT_LEASE_DURATION=30s
PAYMENT_PROVIDER_TIMEOUT=2s
PAYMENT_PROVIDER_MAX_CONCURRENT=4
PAYMENT_CIRCUIT_FAILURE_THRESHOLD=5
PAYMENT_CIRCUIT_OPEN_DURATION=30s
```

批次最坏超时预算按 `ceil(batch_size / worker_concurrency) * provider_timeout` 估算，启动时
必须小于租约时长。Provider 并发上限可以小于 Worker 并发度，此时多出的调用会被舱壁
立即拒绝并回到持久化重试队列。

## 超时演练

在内存模式启动一个每次延迟 500ms、调用超时 100ms 的实例：

```bash
PAYMENT_PROVIDER_TIMEOUT=100ms \
SIMULATED_PAYMENT_DELAY=500ms \
go run ./cmd/api
```

注册、登录并创建订单后，等待 Payment Worker 处理，再查询支付状态与指标：

```bash
curl -s http://127.0.0.1:8080/orders/<order-id>/payment \
  -H 'Authorization: Bearer <access-token>'
curl -s http://127.0.0.1:8080/metrics | grep order_system_payment_provider
```

预期支付为 `unknown`，`requests_total{outcome="timeout"}` 增加。订单不会因为未知结果被
取消。停止进程即可结束演练；内存模式数据会随进程消失。

## 熔断演练

让模拟 Provider 的前五次真实调用失败，并缩短冷却与重试时间：

```bash
PAYMENT_RETRY_BASE=100ms \
PAYMENT_RETRY_MAX=100ms \
PAYMENT_CIRCUIT_FAILURE_THRESHOLD=2 \
PAYMENT_CIRCUIT_OPEN_DURATION=2s \
SIMULATED_PAYMENT_FAILURES=5 \
go run ./cmd/api
```

创建订单后观察指标：

```bash
curl -s http://127.0.0.1:8080/metrics | grep order_system_payment_provider
```

预期先出现两次 `outcome="error"`，随后出现 `reason="circuit_open"` 的快速拒绝；
`circuit_state{state="open"}` 为 1。冷却结束后只放行一个半开探测，模拟失败耗尽后最终
关闭熔断并完成支付。整个过程始终复用原 Payment ID 作为幂等键。

## 生产排障顺序

1. 查看熔断状态与最近状态切换，确认是否处于持续故障或恢复探测。
2. 按 `outcome` 查看调用速率和耗时，区分 timeout、error、invalid_response 与业务 decline。
3. 查看 `concurrency_limit`，判断 Worker 并发、Provider 舱壁和下游配额是否匹配。
4. 查看支付任务积压和最老任务年龄；快速拒绝保护了系统，但积压仍需要容量或故障恢复。
5. 对 `unknown` 支付优先通过稳定幂等键查询或重试，不能直接再次创建一笔新支付。

熔断器不能替代重试、幂等、持久化任务、告警或人工对账；它只负责限制故障影响面。
