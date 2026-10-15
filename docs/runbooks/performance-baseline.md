# 性能基线与容量检查

## 本地执行

确保 Docker Desktop 正常运行，然后执行：

```bash
make performance-check
```

脚本会：

1. 用独立 Compose Project 启动 PostgreSQL、Redis、Redpanda 和 API；
2. 等待 API readiness 通过；
3. 以固定并发压测 `GET /api/v1/products/product-1`，覆盖 HTTP、商品详情查询和 Redis Cache Aside；
4. 以固定并发压测 `GET /healthz`，观察 HTTP 进程的基础上限；
5. 输出总请求数、成功数、错误率、RPS、P50、P95、P99 和最大延迟；
6. 检查默认学习阈值；
7. 删除临时容器、网络和数据卷。

## 参数覆盖

默认配置适合本地和 CI 的快速回归：

```text
商品列表：10 秒、20 并发、至少 50 RPS、P95 <= 500ms
健康检查：5 秒、20 并发、至少 100 RPS、P95 <= 250ms
错误率：<= 1%
```

可以在本地提高压力：

```bash
PERFORMANCE_DURATION=30s \
PERFORMANCE_CONCURRENCY=100 \
PERFORMANCE_MIN_RPS=200 \
PERFORMANCE_MAX_P95=300ms \
make performance-check
```

端口冲突时覆盖独立端口：

```bash
PERFORMANCE_API_PORT=38081 \
PERFORMANCE_POSTGRES_PORT=35433 \
PERFORMANCE_REDIS_PORT=36380 \
PERFORMANCE_KAFKA_PORT=39093 \
make performance-check
```

## 如何解读结果

- RPS 下降且 P95 上升：可能存在 CPU、连接池、锁竞争或外部依赖瓶颈；
- P50 正常但 P95/P99 很高：重点排查长尾请求、GC、数据库慢查询和连接等待；
- 错误率上升：先区分应用返回错误、客户端超时和基础设施不可用；
- `/healthz` 很快但 `/api/v1/products` 变慢：问题更可能在商品查询、Redis、PostgreSQL 或其连接池；
- 两条路径都变慢：先检查 Docker 资源、宿主机负载和 API 进程本身。

不要只看平均延迟，也不要把一次本地通过当作生产容量结论。容量规划必须结合目标 SLO、真实数据规模和阶梯压测。

## 当前边界

默认检查不压测订单创建，因为订单会修改库存、写入 Outbox、触发支付任务；重复执行会改变业务数据，且很难只靠一个固定阈值解释结果。
需要验证写路径时，应先设计数据重置、幂等键和业务结果校验，再单独建立写入场景。
