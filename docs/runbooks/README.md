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
- [`kubernetes-deployment.md`](kubernetes-deployment.md)：Kubernetes 清单检查、Secret 注入、滚动发布、回滚和排查顺序。
- [`event-contracts.md`](event-contracts.md)：事件契约检查、兼容性判断、版本升级和失败排查。
- [`http-api-contracts.md`](http-api-contracts.md)：OpenAPI、稳定错误码、接口兼容性判断和契约失败排查。
- [`database-connection-pool.md`](database-connection-pool.md)：数据库连接池预算、背压指标、饱和告警和调优排查顺序。
- [`structured-logging.md`](structured-logging.md)：JSON 日志级别、Request/Trace 关联、敏感字段保护和排查步骤。
- [`service-level-objectives.md`](service-level-objectives.md)：HTTP 可用性与延迟 SLO、错误预算燃烧率、告警处置和口径变更检查。
- [`runtime-diagnostics.md`](runtime-diagnostics.md)：安全启用 pprof，采集 CPU、heap、goroutine、锁竞争和 Trace，并解释常见排障顺序。
- [`http-input-bounds.md`](http-input-bounds.md)：检查 JSON body、HTTP Header 的资源上限，并区分 400、413 和 431。
- [`go-fuzz-tests.md`](go-fuzz-tests.md)：运行 Go Fuzz Test、保存最小回归样本并解释 fuzz 通过的边界。
- [`database-query-plans.md`](database-query-plans.md)：运行 PostgreSQL 查询计划回归、阅读索引/顺序扫描，并排查 N+1。
