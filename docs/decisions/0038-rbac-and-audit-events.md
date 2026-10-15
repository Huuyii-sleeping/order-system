# ADR 0038：认证、授权和审计使用独立边界

## 背景

有效 Access Token 只能证明请求者是谁，不能证明其可以读取审计日志或执行消息恢复。把
“已登录”等同于“有权限”会让普通用户获得管理能力；只在普通日志里记录管理请求，则会受到
日志采样、保留周期和格式变化影响，无法形成稳定审计证据。

## 决策

1. 用户角色固定为 `customer`、`operator`、`admin`。注册始终创建 `customer`，客户端不能在
   注册 JSON 中声明角色。
2. Session 认证恢复 `Principal{UserID, Role}`；授权策略再把角色映射到细粒度 Permission。
   当前 `operator` 可读取审计和执行消息恢复，`admin` 拥有全部管理权限。
3. 管理接口使用“认证 → 授权 → 审计 → Handler”的顺序。允许和拒绝的授权决策都写入
   `audit_events`；审计写入失败时 fail closed，不执行敏感 Handler。
4. 审计事件只保存低敏事实：Actor、Action、Resource、Outcome、Request ID、Role、Permission
   和时间，不保存 Token、Cookie、请求体或 Provider 原始响应。
5. 角色变更通过数据库凭据保护的 `cmd/accessops role-set` 完成，角色更新与审计事件位于同一
   PostgreSQL 事务。公共注册和普通业务 API 不提供提权入口。

## 影响与边界

- RBAC 适合当前角色较少的系统；未来出现组织、项目、订单归属等动态关系时，应在 Permission
  之后增加资源级策略，而不是无限增加角色。
- HTTP 授权审计记录的是“是否允许进入 Handler”，业务写操作仍应在自身事务中记录最终结果。
- 审计表需要独立保留、访问控制和防篡改策略；当前学习项目依靠应用权限和数据库约束，尚未
  使用独立审计数据库或 WORM 存储。
