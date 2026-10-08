# 优雅关闭演练

## 目标

验证实例收到 `SIGTERM` 后先退出 readiness，再停止 HTTP 和后台组件，而不是直接丢弃在途工作。

## 本地快速演练

在第一个终端启动内存模式，并缩短排水延迟以便观察：

```bash
SHUTDOWN_DRAIN_DELAY=3s SHUTDOWN_TIMEOUT=10s go run ./cmd/api
```

第二个终端先确认两个探针都成功，然后向应用进程发送一次 `SIGTERM`。使用编译后的二进制
演练最容易拿到准确 PID：

```bash
go build -o /tmp/order-system-api ./cmd/api
SHUTDOWN_DRAIN_DELAY=3s SHUTDOWN_TIMEOUT=10s /tmp/order-system-api
kill -TERM <pid>
```

在 3 秒排水窗口内查询：

```bash
curl -i http://127.0.0.1:8080/readyz
curl -i http://127.0.0.1:8080/healthz
```

预期 `/readyz` 返回 `503` 和 `unavailable`，`/healthz` 仍返回 `200`。排水延迟结束后 HTTP
端口关闭，日志依次出现 `shutdown signal received`、`service draining`、
`http server shutting down` 和 `service stopped`。进程正常退出码为 0。

## 在途工作验收

自动化测试用阻塞桩验证了以下顺序：

- Outbox 已认领消息在停止 Claim 后仍完成发布和确认。
- Payment Worker 已认领支付在停止 Claim 后仍得到终态。
- Kafka 已进入 Handler 的消息在停止 Poll 后仍完成业务并提交 offset。
- 后台任务 panic 或意外退出会传给主进程，而不是静默留下一个残缺实例。

## Docker 演练

启动包含 PostgreSQL、Redis、Redpanda、迁移任务和 API 的完整容器环境：

```bash
make app-up
docker compose --profile application stop api
```

`docker compose stop` 会向 API 发送 `SIGTERM`，并按照 `stop_grace_period` 等待。查看
`docker compose logs api`，应能看到完整排水日志；`docker inspect` 中容器退出码应为 0。
镜像通过多阶段构建只复制两个静态 Go 二进制，并使用非 root 用户运行。

## 生产配置注意事项

容器平台的终止宽限期应满足：

```text
termination grace period > SHUTDOWN_TIMEOUT + telemetry/resource cleanup allowance
SHUTDOWN_TIMEOUT > SHUTDOWN_DRAIN_DELAY
```

排水延迟还要覆盖 readiness 探针和负载均衡器的状态传播时间。若进程在
`SHUTDOWN_TIMEOUT` 内无法结束，应用会取消在途 Context；未完成任务由持久化租约过期后重新
认领，Kafka 消息则因 offset 未提交而重新投递。不要通过无限增大关闭时间掩盖不遵守 Context
的驱动或 SDK。
