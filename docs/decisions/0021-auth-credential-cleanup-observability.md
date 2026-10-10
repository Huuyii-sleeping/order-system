# 0021：认证凭证清理的指标与告警

## 背景

清理 Worker 即使没有退出，也可能持续失败、执行变慢或长时间没有成功运行。
仅看进程存活和日志不容易发现清理积压，因此需要把维护任务的健康状态暴露给
Prometheus。

## 决策

新增以下低基数指标：

- `order_system_auth_credential_cleanup_runs_total{outcome}`：批次成功、失败或关闭取消次数。
- `order_system_auth_credential_cleanup_deleted_rows_total{kind}`：删除的 sessions 和 refresh_tokens 行数。
- `order_system_auth_credential_cleanup_duration_seconds{outcome}`：每个批次的耗时分布。
- `order_system_auth_credential_cleanup_last_success_timestamp_seconds`：最近成功批次的 Unix 时间。

`outcome` 和 `kind` 只使用固定枚举，不包含 token hash、用户 ID、错误文本或任务 ID。
Worker 通过应用端口上报，Prometheus 适配器负责注册和导出指标。

## 告警

- 最近 15 分钟出现清理失败，持续 5 分钟后告警。
- 最近成功时间超过两小时，持续 10 分钟后告警。

第二条规则用于捕获任务静默停止、调度异常或持续失败导致的“没有成功记录”状态。
规则同时使用 `absent(...)` 兜底，覆盖 Worker 从未启动或从未完成首个成功批次的情况。
阈值是本地学习基线，生产环境应结合清理周期、批量大小和数据增长速度调整。

## 重新评估条件

- 如果实例级指标无法反映集群整体清理进度，应增加数据库级过期积压量查询或独立维护 Job 指标。
- 如果删除量标签出现动态值，必须在评审中拒绝，避免高基数时间序列。
