# 0003：短期 Access Token 与 Refresh Token 轮换

## 背景

订单 API 需要一个可放在 Authorization Bearer 中的短期凭证，同时登录状态
不能因为 Access Token 过期而要求用户频繁输入密码。

## 决策

- Access Token 仍然是服务端保存摘要的 Session，默认有效 15 分钟，只能用于
  认证业务 API。
- Refresh Token 默认有效 30 天，只能调用 POST /auth/refresh，服务端只保存
  摘要，不接受它作为订单 API 的 Bearer Token。
- Refresh Token 放在 HttpOnly Cookie 中，降低被页面脚本直接读取的风险。
- 每个登录产生一个凭证 family_id，其中包含 Access Session 和 Refresh Token。
- 刷新使用 PostgreSQL 行锁和事务：旧 Refresh Token 标记为 used_at，同时写入
  新 Access Session 和新 Refresh Token。
- 已使用的 Refresh Token 再次出现时视为重放，撤销整个 family，阻止攻击者继续
  使用可能已经被窃取的替代凭证。
- logout 撤销当前凭证 family，logout-all 撤销用户的全部凭证 family。

## 为什么不删除旧 Refresh Token

删除只能说明“查不到”，不能区分“从未存在”和“已经被消费”。保留摘要并记录
used_at，才能识别旧 Token 重放并触发 family 级别的安全处置。

## 仍未覆盖

本节没有加入跨站请求伪造防护策略、设备管理页面、刷新限流和清理过期 Token
的后台任务；这些会在后续安全与可运维性章节处理。
