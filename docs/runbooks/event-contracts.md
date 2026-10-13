# 事件契约维护手册

## 执行检查

运行契约测试（包含事件和 HTTP API）：

在仓库根目录执行：

```bash
make contract-check
```

事件契约用例位于 `tests/contract/events_test.go`；同一命令中的 HTTP 契约说明见
`docs/runbooks/http-api-contracts.md`。

完整 CI 仍然使用：

```bash
make ci
```

契约检查不需要 PostgreSQL、Redis、Kafka 或 Kubernetes 集群。

## 修改事件前的判断

1. 先确认生产者和所有消费者依赖的 Topic 与版本。
2. 判断是增加可选字段，还是改变/删除现有字段。
3. 检查新字段是否包含 Provider reference、原始错误、凭据或其他不应跨模块传播的信息。
4. 如果是破坏性变化，创建新的 `V2` 类型和 Topic，不要直接修改 `V1` 的语义。
5. 更新 `tests/contract/events_test.go`，让公开 JSON 变化必须经过显式审查。
6. 同时更新生产者、消费者、集成事件文档和必要的重放策略。

## 排查契约测试失败

```text
Topic 名称变化
  → 检查是否误改版本，还是确实需要 v2
字段集合变化
  → 判断新增字段是否兼容，删除/改类型是否破坏旧消费者
敏感字段测试失败
  → 移除 Provider 内部响应，不把它变成公共事件契约
业务消费者测试失败
  → 区分 JSON 形状问题和业务校验/状态机问题
```

契约测试通过，只代表事件形状符合当前约定；它不代表消息已经成功发布，也不代表消费者的
事务、幂等和重试流程一定正确。
