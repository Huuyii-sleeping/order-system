# 通知投递运行手册

## 本地启动

只验证 Go 内存链路：

    go test ./internal/application/notification ./internal/app ./internal/transport/httpapi

启动 PostgreSQL 并执行迁移：

    make db-up

执行 PostgreSQL 通知集成测试：

    make test-integration

该测试会创建临时 schema，验证通知 Outbox、Inbox、任务租约、Provider 重试和客户隔离，测试结束后自动删除 schema。

## 故障演练

应用使用模拟 Provider，可以通过环境变量制造延迟、网络错误或明确拒绝：

    SIMULATED_NOTIFICATION_DELAY=3s SIMULATED_NOTIFICATION_FAILURES=1 go run ./cmd/api

默认 Provider 超时是 NOTIFICATION_PROVIDER_TIMEOUT=2s。调用超时会把通知置为 unknown，任务按 NOTIFICATION_RETRY_BASE 和 NOTIFICATION_RETRY_MAX 指数退避，不会直接判定为发送失败。

明确拒绝可以使用：

    SIMULATED_NOTIFICATION_REJECTIONS=1 go run ./cmd/api

明确拒绝进入 failed，不会因为同一个可重复的业务拒绝而无限重试。

## 查询通知状态

通知查询接口需要登录后的认证信息：GET /orders/{orderID}/notification

响应中的 status 可能是：

- pending：通知任务已创建，等待 Worker 认领；
- processing：Worker 已认领，正在调用 Provider；
- unknown：网络错误或超时，等待使用同一幂等键重试；
- sent：Provider 明确确认发送；
- failed：Provider 明确拒绝。

接口只返回安全的业务状态，不返回 Provider reference 和原始失败原因。

## 排查顺序

1. 检查 GET /readyz 和数据库连接，确认实例是否已经进入 draining。
2. 查询 notifications.status、notification_tasks.attempts、next_attempt_at、last_error，确认任务是否积压、是否持续处于 unknown。
3. 查看 order_system_notification_tasks_total 与 order_system_notification_retries_total 指标，按 outcome/reason 聚合，避免按 notification ID 创建高基数标签。
4. 检查 Provider 延迟、错误比例和外部服务状态；不要因为一次超时就手工把通知改成 failed，因为请求可能已经在 Provider 侧成功。
5. 如果任务租约过期，允许其他 Worker 重新认领。重复调用必须复用 Notification ID 幂等键。

## 数据库验收查询

    SELECT id, order_id, status, created_at, updated_at FROM notifications ORDER BY created_at DESC LIMIT 20;

    SELECT id, notification_id, attempts, next_attempt_at, locked_until, completed_at, last_error FROM notification_tasks ORDER BY next_attempt_at DESC LIMIT 20;

不要直接删除 unknown 任务来“清理告警”。先确认 Provider 是否已经发送，再决定是否通过受控的补偿或对账流程处理；本地学习项目没有提供跳过幂等保护的管理员接口。
