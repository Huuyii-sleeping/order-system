# 认证凭证清理运行手册

## 作用

API 会启动一个 `auth-credential-cleanup` 后台任务，定期物理删除已经过期的
Access Session 和 Refresh Token。认证请求本身仍然会实时检查有效期，清理任务不是
认证正确性的唯一保障。

默认配置：

- `AUTH_CREDENTIAL_CLEANUP_INTERVAL=1h`
- `AUTH_CREDENTIAL_CLEANUP_BATCH_SIZE=500`

## 本地验证

先启动 PostgreSQL 并执行迁移：

```bash
make db-up
make test-integration
```

集成测试会在隔离 Schema 中写入已过期和仍有效的凭证，验证每批最多删除指定数量，
并确认有效凭证仍可查询。测试结束后会删除隔离 Schema。

也可以在内存模式观察启动日志：

```bash
AUTH_CREDENTIAL_CLEANUP_INTERVAL=10s \
AUTH_CREDENTIAL_CLEANUP_BATCH_SIZE=50 \
go run ./cmd/api
```

只有实际删除记录时才会输出 `expired credentials cleaned`；没有过期记录时不产生
周期性成功日志，避免日志噪声。

## 故障排查

如果看到 `expired credential cleanup failed`：

1. 检查 `/readyz` 和 PostgreSQL 连接是否正常。
2. 检查 `sessions_expires_at_idx` 与 `refresh_tokens_expires_at_idx` 是否存在。
3. 查看数据库锁等待和单批删除耗时；积压严重时临时调小批次或缩短周期。
4. 修复依赖后等待下一轮，Worker 会继续运行，不需要重启 API 才能恢复清理。

关闭服务时，排水阶段会停止新的清理批次；已经开始的 SQL 会在关闭预算内完成，
预算耗尽后才由 `workCtx` 取消。

停止本地数据库但保留数据卷：

```bash
make db-down
```
