# 运行手册

这里记录本地启动、依赖检查、故障模拟、数据迁移、回滚和常见问题处理方式。

当前运行手册：

- [`local-observability.md`](local-observability.md)：本地 Prometheus、告警与 Grafana
  的启动、验收和故障模拟。
- [`graceful-shutdown.md`](graceful-shutdown.md)：SIGTERM、readiness 排水和在途工作关闭演练。
- [`notification-delivery.md`](notification-delivery.md)：通知任务、Provider 故障模拟、状态查询和排查顺序。
- [`auth-credential-cleanup.md`](auth-credential-cleanup.md)：过期 Session/Refresh Token 的批量清理、故障排查和关闭行为。
- [`integration-and-fault-drills.md`](integration-and-fault-drills.md)：隔离运行 PostgreSQL、Redis、Redpanda、完整 API 的集成验收和 readiness 故障演练。
- [`database-migration-and-recovery.md`](database-migration-and-recovery.md)：迁移不可变规则、Expand/Contract、备份恢复验收和应用回滚处置矩阵。
- [`performance-baseline.md`](performance-baseline.md)：隔离基础设施中的 HTTP 性能基线、阈值、参数覆盖和结果解读。
- [`rollout-and-rollback.md`](rollout-and-rollback.md)：灰度发布、readiness 门禁、回滚演练和生产排查顺序。
