# 生产就绪验收矩阵

## 一键自动验收

在仓库根目录执行：

```bash
make production-readiness-check
```

该命令会创建并清理隔离的容器、数据卷、临时数据库和带进程号的临时镜像；不会使用普通开发
Compose Project。任一门禁失败都会停止，终端保留失败步骤和相关服务日志。

## 自动化矩阵

| 维度 | 可重复证据 | 阻断条件 |
| --- | --- | --- |
| 依赖完整性 | `go mod verify` | module checksum 不一致 |
| 代码正确性 | `make ci`：格式、单测、Race、Vet | 任一检查失败 |
| 协议兼容 | HTTP/Event contract tests，v1/legacy 路由探测 | 路由、字段、安全或错误码漂移 |
| 可观测配置 | Prometheus 配置/规则/规则用例、Tempo 校验 | 配置或告警规则无效 |
| 部署清单 | `kubectl kustomize` | Kubernetes 清单不能渲染 |
| 发布制品 | Docker build、`USER app`、非 root UID、两个可执行文件 | 镜像不可构建或以 root 运行 |
| 真实依赖 | PostgreSQL、Redis、Redpanda 集成和 `/api/v1` E2E | 订单完整链路失败 |
| 故障隔离 | 逐个停止依赖，验证 `/readyz=503`、`/healthz=200` 和恢复 | 摘流或恢复语义错误 |
| 数据恢复 | migration checksum、`pg_dump`、全新库 restore、数据指纹 | 迁移漂移或恢复不一致 |
| 性能基线 | 隔离的 RPS、P95、错误率阈值 | 任一阈值越界 |
| 发布安全 | old/new 双实例、canary 请求、停止 new、old 继续服务 | 灰度或回滚冒烟失败 |

## 自动化无法代替的生产签字

真正上线前，发布记录还必须明确确认：

- 生产域名、TLS、网关鉴权、WAF、限额和 Secret 管理；
- 生产数据库规格、连接上限、PITR、备份加密/保留、跨区恢复和实际 RPO/RTO 演练；
- Kafka Topic 的分区、复制因子、保留期、ACL 和容量；
- 支付/通知 Provider 的生产幂等、查询、Webhook、配额、超时和对账能力；
- 同规格压测、峰值模型、容量余量、扩缩容策略和下游全局限额；
- Dashboard、告警路由、值班负责人、SLO/Error Budget 和事故升级路径；
- 数据分级、审计保留、防篡改、隐私、许可证与安全评审；
- 调用方完成 `/api/v1` 迁移，legacy 指标在批准的观察窗口归零。

这些项目缺少任意一项时，应把结论写成“自动门禁通过，生产签字未完成”，不能写“生产已就绪”。

## 失败后的处理

1. 先定位第一项失败门禁，不要通过调低阈值或删除测试直接放行。
2. 使用该门禁对应的独立 Runbook 复现和修复。
3. 修复后先运行最小定向检查，再重新运行完整 `make production-readiness-check`。
4. 保存 CI URL、commit SHA、镜像 digest、数据库恢复结果和外部签字到发布记录；聊天结论不是发布证据。
