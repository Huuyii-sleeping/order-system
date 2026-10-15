# 权限与审计操作

## 权限模型

| 角色 | 普通订单能力 | 读取审计 | 消息恢复 |
| --- | --- | --- | --- |
| `customer` | 自己的订单 | 否 | 否 |
| `operator` | 自己的订单 | 是 | 是 |
| `admin` | 自己的订单 | 是 | 是 |

角色不是身份。Access Token 先恢复用户身份和角色，Policy 再针对具体 Permission 作决定。
订单所有权检查仍由订单查询条件负责，不能因为加入 RBAC 就删除资源级隔离。

## 引导管理员或操作员

角色不能通过注册参数设置。使用有数据库访问权限的受控终端执行：

```bash
DATABASE_URL='<database-url>' go run ./cmd/accessops role-set \
  --user-id '<user-id>' \
  --role operator \
  --actor 'bootstrap-operator-id' \
  --reason 'approved access request OPS-200'
```

用户角色更新与 `identity.role.change` 审计事件在同一个事务中提交；任一步失败都会全部回滚。
`reason` 只写变更依据，不得包含密码、Token、Cookie 或用户隐私。

## 查询审计事件

使用具有 `audit:read` 权限的 Access Token：

```bash
curl 'http://127.0.0.1:8080/api/v1/admin/audit-events?limit=50' \
  -H 'Authorization: Bearer <operator-access-token>'
```

- 未认证返回 `401`。
- 已认证但无权限返回 `403 permission_denied`，拒绝决策也会审计。
- 审计数据库不可用时返回 `500`，并且敏感 Handler 不会执行。
- `limit` 范围为 1 到 100，避免无界读取审计表。

## 排查顺序

1. `401`：检查 Session 是否存在和过期，不要先改角色。
2. `403`：通过受控数据库查询核对用户角色和 Permission 矩阵。
3. `500`：检查 PostgreSQL 可用性和 `audit_events` 迁移；这是 fail-closed，不应绕过审计。
4. 找不到事件：使用 Request ID、Actor、Action 和时间范围交叉检查，普通访问日志不是审计表的
   替代品。
