# 集成验收与依赖故障演练

## 目标

一条命令验证：

- PostgreSQL 事务、行锁、幂等、Outbox/Inbox 和任务租约；
- Redis 跨实例缓存和共享限流；
- Redpanda/Kafka 发布、消费、offset 提交和 DLQ；
- HTTP 注册登录到订单确认、支付成功和通知成功的完整链路；
- PostgreSQL、Redis 或 Broker 不可用时，liveness 与 readiness 的真实行为。

## 执行

确保 Docker Desktop 正常运行，然后执行：

```bash
make integration-check
```

脚本使用独立的 `order-system-integration-<pid>` Compose Project，默认占用：

- API：`127.0.0.1:28080`
- PostgreSQL：`127.0.0.1:25432`
- Redis：`127.0.0.1:26379`
- Redpanda Kafka：`127.0.0.1:29092`

如果端口已经被占用，可以只为本次命令覆盖：

```bash
INTEGRATION_API_PORT=38080 \
INTEGRATION_POSTGRES_PORT=35432 \
INTEGRATION_REDIS_PORT=36379 \
INTEGRATION_KAFKA_PORT=39092 \
make integration-check
```

## 执行顺序

```text
隔离启动完整容器栈
        ↓
PostgreSQL / Redis / Kafka 适配器测试
        ↓
HTTP 黑盒订单生命周期 E2E
        ↓
依次停止 Redis、Redpanda、PostgreSQL
        ↓
验证 healthz=200、readyz=503
        ↓
恢复依赖并验证 readyz=200
        ↓
删除本次临时容器、网络、数据卷和本地构建镜像
```

无论成功、失败还是中断，脚本都会尝试清理本次创建的临时资源。失败时会先打印 API、
PostgreSQL、Redis 和 Redpanda 日志。它不会删除普通 `order-system` Compose Project
使用的开发数据卷或镜像。

## 单独排查

已经手工启动相应依赖时，可以缩小范围：

```bash
make test-integration
make test-redis-integration
make test-kafka-integration
make test-e2e
```

这些目标默认连接 README 中的本地端口。单独运行 `make test-e2e` 前，必须先启动完整
API；如果只启动内存模式，它无法证明 PostgreSQL 和 Kafka 链路。

## 结果解释

- 单元测试通过、适配器测试失败：优先检查真实 SQL、事务、Redis 命令或 Kafka 协议。
- 适配器测试通过、E2E 失败：优先检查路由、认证、配置、依赖注入和后台 Worker 组装。
- `/healthz` 与 `/readyz` 都失败：应用进程可能已经退出，不只是依赖故障。
- `/healthz` 为 `200`、`/readyz` 为 `503`：进程仍活着，但不应继续接收新流量。
- 依赖恢复后 readiness 仍不恢复：检查客户端重连、连接池和后台组件日志。
