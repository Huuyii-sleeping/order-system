# 架构决策记录

重大设计取舍放在这里记录，避免只存在于聊天中。

建议每条记录说明：

- 背景和待解决的问题
- 备选方案
- 最终决定
- 选择原因
- 影响和遗留风险
- 重新评估的条件

当前新增：

- [`0016-notification-delivery.md`](0016-notification-delivery.md)：通知 Outbox、Inbox、Task、Provider 幂等键和最终一致投递。
- [`0017-provider-reconciliation.md`](0017-provider-reconciliation.md)：通过 Provider 查询对账收敛超时和 Worker 崩溃造成的不确定结果。
- [`0018-notification-provider-webhook.md`](0018-notification-provider-webhook.md)：通过 HMAC Webhook 与持久化事件幂等主动收敛通知结果。
- [`0019-order-keyset-pagination.md`](0019-order-keyset-pagination.md)：订单历史使用复合游标、Keyset Pagination 和匹配查询形状的索引。
- [`0020-expired-credential-cleanup.md`](0020-expired-credential-cleanup.md)：过期认证凭证使用有界批量删除和独立维护 Worker 回收。
- [`0021-auth-credential-cleanup-observability.md`](0021-auth-credential-cleanup-observability.md)：为认证凭证维护任务增加低基数指标、最近成功时间和 Prometheus 告警。
- [`0022-ci-release-gates.md`](0022-ci-release-gates.md)：通过 CI 分层执行 Go、观测配置和容器构建门禁，保持检查与部署的授权边界。
- [`0023-real-infrastructure-integration.md`](0023-real-infrastructure-integration.md)：使用隔离容器执行真实适配器测试、HTTP 黑盒 E2E 和依赖故障演练。
- [`0024-forward-migrations-and-recovery.md`](0024-forward-migrations-and-recovery.md)：使用迁移 checksum、Expand/Contract 和真实备份恢复建立安全发布回滚边界。
- [`0025-capacity-baseline-and-load-check.md`](0025-capacity-baseline-and-load-check.md)：使用隔离基础设施建立固定并发 HTTP 性能基线，区分回归检查与生产容量规划。
- [`0026-canary-rollout-and-rollback.md`](0026-canary-rollout-and-rollback.md)：通过 readiness、灰度流量和旧实例保活建立应用发布与回滚边界。
- [`0027-kubernetes-deployment-baseline.md`](0027-kubernetes-deployment-baseline.md)：通过 Kustomize、探针、滚动更新、PDB 和 Secret 边界把应用发布基线交给 Kubernetes。
- [`0028-event-contract-testing.md`](0028-event-contract-testing.md)：通过版本化 Topic、JSON 契约测试和敏感字段边界保护跨模块事件。
- [`0029-http-api-contract-and-errors.md`](0029-http-api-contract-and-errors.md)：通过 OpenAPI、真实路由探测和稳定错误码保护 HTTP 客户端契约。
- [`0030-database-connection-pool.md`](0030-database-connection-pool.md)：为单实例数据库连接建立显式预算、池满背压指标和持续饱和告警。
- [`0031-structured-logging-and-correlation.md`](0031-structured-logging-and-correlation.md)：统一 JSON 日志、Context 关联字段、日志级别和敏感信息边界。
- [`0032-service-level-objectives-and-error-budgets.md`](0032-service-level-objectives-and-error-budgets.md)：用用户视角 SLI、28 天 SLO、错误预算和多窗口燃烧率定义 HTTP 可靠性。
- [`0033-secure-runtime-diagnostics.md`](0033-secure-runtime-diagnostics.md)：通过默认关闭、独立回环端口和受监管生命周期安全提供 Go pprof 运行时诊断。
