# 数据库迁移、备份恢复与应用回滚

## 本地恢复验收

确保 Docker Desktop 正常运行，然后执行：

```bash
make database-recovery-check
```

脚本使用独立 `order-system-recovery-<pid>` Compose Project 和临时目录：

1. 启动干净 PostgreSQL；
2. 运行全部 forward migration；
3. 写入虚构用户、商品、库存、订单和 Outbox 数据；
4. 使用 custom format 执行 `pg_dump`；
5. 创建全新的 `order_system_restore` 数据库；
6. 使用 `pg_restore --exit-on-error` 恢复；
7. 在恢复库重新运行迁移器，验证版本、文件名和 checksum；
8. 对比源库与恢复库的迁移数量和业务数据指纹；
9. 删除临时数据库容器、数据卷和备份文件。

默认只占用 `127.0.0.1:35432`，端口冲突时可以覆盖：

```bash
RECOVERY_POSTGRES_PORT=45432 make database-recovery-check
```

备份文件只存在于 `mktemp` 创建的临时目录，不会写入仓库。

## 迁移文件规则

- 已应用的 `migrations/*.sql` 永远不修改、不重命名、不删除；
- 修改已有结构时创建下一个递增版本；
- checksum mismatch 表示迁移历史不可信，应停止发布并确认 Git 历史和目标数据库，不能
  手工修改 `schema_migrations.checksum` 让检查通过；
- 多个迁移进程可以同时启动，但 PostgreSQL advisory lock 会让它们串行执行；
- DDL 和版本记录在同一个事务中，失败不会留下“结构变了但版本没记”的半完成状态。

## Expand/Contract 发布顺序

以字段替换为例：

```text
Release A：Expand
新增可空字段或新表，旧应用仍可运行
        ↓
Release B：Migrate
新应用双写，后台回填历史数据，读取兼容新旧字段
        ↓
Release C：Switch
确认回填完成，读取切换到新字段，但暂时保留旧字段
        ↓
Release D：Contract
确认没有旧应用实例后，再删除旧字段
```

Release B 或 C 出现应用问题时，可以回滚应用镜像，因为旧字段还在。数据库不需要立即
执行反向迁移。

## 故障处置矩阵

| 场景 | 首选动作 | 不应直接做 |
|---|---|---|
| 迁移事务执行失败 | 修复新迁移后重新执行 | 手工伪造迁移版本 |
| 新应用有 Bug、迁移仍向后兼容 | 回滚应用镜像，保留数据库结构 | 恢复旧备份覆盖新订单 |
| 已应用迁移 checksum 漂移 | 停止发布，恢复 Git 中原文件 | 修改数据库 checksum 放行 |
| 数据被错误更新或删除 | 停止相关写入，PITR/恢复到新库并对账 | 在活动主库上盲目 `pg_restore` |
| 旧结构需要最终删除 | 新增后续 contract migration | 修改已经执行的 expand migration |

## 恢复演练与生产恢复的区别

本地脚本证明“当前逻辑备份可以被当前版本恢复”，但生产环境还必须定义：

- RPO：最多可以接受丢失多少时间的数据；
- RTO：发生事故后必须在多久内恢复服务；
- PITR：是否能恢复到事故发生前的指定时间点；
- 恢复后的数据对账、DNS/连接串切换和旧主库隔离流程。

备份只有经过定期恢复演练，才能被视为可用备份。
