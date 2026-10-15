# 通知投递运行手册

## 本地启动

只验证 Go 内存链路：

    go test ./internal/application/notification ./internal/app ./internal/transport/httpapi

启动 PostgreSQL 并执行迁移：

    make db-up

执行 PostgreSQL 通知集成测试：

    make test-integration

该测试会创建临时 schema，验证通知 Outbox、Inbox、任务租约、Provider 重试和客户隔离，测试结束后自动删除 schema。

## Provider Webhook

设置至少 32 字节的本地测试密钥后启动服务；未设置时 Webhook 路由不会注册：

    NOTIFICATION_WEBHOOK_SECRET=replace-with-a-random-secret-at-least-32-bytes go run ./cmd/api

Provider 请求格式：

    POST /webhooks/notifications/provider
    Content-Type: application/json
    X-Webhook-Timestamp: <Unix 秒>
    X-Webhook-Signature: <HMAC-SHA256 十六进制>

签名原文必须是 `timestamp + "." + rawBody`。不要先解析再重新序列化 JSON，否则空格或字段顺序变化会导致签名不一致。默认只接受当前时间前后 5 分钟内的请求，可用 `NOTIFICATION_WEBHOOK_TOLERANCE` 调整。

请求体示例：

    {"event_id":"provider-event-1","notification_id":"notification-1","idempotency_key":"notification-1","status":"succeeded","provider_reference":"provider-reference-1"}

本地可使用以下命令生成签名并发送回调。示例值是明显的本地占位值，不要替换或记录生产 Secret：

    WEBHOOK_SECRET=replace-with-a-random-secret-at-least-32-bytes
    WEBHOOK_TIMESTAMP=$(date +%s)
    WEBHOOK_BODY='{"event_id":"provider-event-1","notification_id":"notification-1","idempotency_key":"notification-1","status":"succeeded","provider_reference":"provider-reference-1"}'
    WEBHOOK_SIGNATURE=$(printf '%s' "${WEBHOOK_TIMESTAMP}.${WEBHOOK_BODY}" | openssl dgst -sha256 -hmac "${WEBHOOK_SECRET}" -hex | awk '{print $NF}')
    curl -i -X POST http://127.0.0.1:8080/api/v1/webhooks/notifications/provider \
      -H 'Content-Type: application/json' \
      -H "X-Webhook-Timestamp: ${WEBHOOK_TIMESTAMP}" \
      -H "X-Webhook-Signature: ${WEBHOOK_SIGNATURE}" \
      --data "${WEBHOOK_BODY}"

首次处理返回 `processed: true`；完全相同的重试返回 200 和 `processed: false`。相同 `event_id` 携带不同请求体，或试图用相反结果覆盖终态，会返回 409。签名错误或时间戳超过容忍窗口返回 401。

## 故障演练

应用使用模拟 Provider，可以通过环境变量制造延迟、网络错误或明确拒绝：

    SIMULATED_NOTIFICATION_DELAY=3s SIMULATED_NOTIFICATION_FAILURES=1 go run ./cmd/api

默认 Provider 超时是 NOTIFICATION_PROVIDER_TIMEOUT=2s。调用超时会把通知置为 unknown，任务按 NOTIFICATION_RETRY_BASE 和 NOTIFICATION_RETRY_MAX 指数退避，不会直接判定为发送失败。

明确拒绝可以使用：

    SIMULATED_NOTIFICATION_REJECTIONS=1 go run ./cmd/api

明确拒绝进入 failed，不会因为同一个可重复的业务拒绝而无限重试。

模拟“Provider 已发送成功，但响应丢失”：

    SIMULATED_NOTIFICATION_RESPONSE_LOSSES=1 NOTIFICATION_RETRY_BASE=1s go run ./cmd/api

第一次调用会在 Provider 内保存成功结果后返回错误，本地状态先进入 unknown。下一轮 Worker
按同一个幂等键调用 Lookup，查询到成功后直接完成任务，不会再次调用 Send。这同时模拟了
进程在 Provider 成功后、本地事务提交前崩溃的恢复方式。

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
3. 查看 order_system_notification_tasks_total、order_system_notification_retries_total、order_system_notification_reconciliations_total 与 order_system_notification_webhooks_total；Webhook 只使用 processed、duplicate、invalid_signature、stale、invalid_payload、not_found、conflict、error 等低基数结果标签。
4. 检查 Provider 延迟、错误比例和外部服务状态；不要因为一次超时就手工把通知改成 failed，因为请求可能已经在 Provider 侧成功。
5. 如果任务租约过期，允许其他 Worker 重新认领。unknown 或过期 processing 任务会先查询 Provider；只有查询明确未找到时才复用 Notification ID 幂等键重发。

如果 reconciliation 的 error 持续增长，优先检查 Provider 查询 API；不要因为查询不可用就切换为盲目 Send。若 not_found 持续增长，需要确认 Provider 查询是否最终一致、是否存在保留期限，以及重发是否仍受幂等键保护。

## 数据库验收查询

    SELECT id, order_id, status, created_at, updated_at FROM notifications ORDER BY created_at DESC LIMIT 20;

    SELECT id, notification_id, attempts, next_attempt_at, locked_until, completed_at, last_error FROM notification_tasks ORDER BY next_attempt_at DESC LIMIT 20;

    SELECT event_id, notification_id, received_at, processed_at FROM notification_webhook_events ORDER BY processed_at DESC LIMIT 20;

不要直接删除 unknown 任务来“清理告警”。先确认 Provider 是否已经发送，再决定是否通过受控的补偿或对账流程处理；本地学习项目没有提供跳过幂等保护的管理员接口。
