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
