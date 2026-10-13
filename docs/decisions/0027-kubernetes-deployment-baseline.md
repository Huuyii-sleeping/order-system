# Kubernetes 部署基线

## 背景

第 26 节的本地脚本验证了“旧实例继续服务、新实例先 readiness、异常时停止新实例”的发布边界，
但它没有真正的编排平台。生产环境还需要一个系统负责维护副本数、滚动替换、服务发现、探针和
节点维护时的中断边界。

同时，应用进程已经实现了 `/healthz`、`/readyz` 和 `SIGTERM` 优雅关闭。如果 Kubernetes 清单
没有正确使用这些能力，平台可能在实例还没准备好时发送流量，或者在后台任务尚未排水时强行杀死
容器，前面的应用层设计就无法落地。

## 决定

- 在 `deployments/kubernetes/base` 提供 Kustomize base，而不是绑定某一个云厂商或真实集群；
- API 使用两个副本、`maxUnavailable: 0` 和 `maxSurge: 1`，新 Pod readiness 通过后才逐步替换旧 Pod；
- `startupProbe` 和 `livenessProbe` 使用 `/healthz`，`readinessProbe` 使用 `/readyz`；
- `terminationGracePeriodSeconds` 大于应用的关闭预算，让应用有时间排水 HTTP 请求和后台 Worker；
- 通过 `PodDisruptionBudget` 保证自愿驱逐时至少保留一个可服务副本；
- 非敏感运行参数放入 ConfigMap，数据库/Redis 连接串和 Webhook 密钥只通过外部创建的 Secret 注入；
- 容器使用非 root、只读根文件系统、禁止提权、丢弃 Linux capabilities，并关闭 ServiceAccount token 自动挂载；
- 基线只描述 API Deployment，不在仓库里伪造 PostgreSQL、Redis、Kafka 的生产 StatefulSet；这些依赖应由
  独立基础设施或受控平台提供；
- 数据库迁移不由 API Deployment 启动时自动执行。迁移必须作为独立、可审计的发布步骤完成，再放量应用；
- `make kubernetes-check` 只做离线 Kustomize 渲染，不声称已经连接或验收任何 Kubernetes 集群。

## 发布关系

```text
构建并固定镜像 digest
        ↓
准备 order-system-runtime Secret
        ↓
执行并确认数据库迁移
        ↓
kubectl apply -k deployments/kubernetes/base
        ↓
等待 Deployment rollout + /readyz
        ↓
通过入口层逐步放量并观察指标
        ↓
异常时暂停放量或回滚 Deployment 镜像
```

Deployment 的滚动更新只解决实例替换，不自动解决数据库兼容性。数据库仍然必须遵守
Expand/Contract；应用回滚前要确认旧镜像能够读取当前数据库结构和数据。

## 为什么 Secret 不提交到仓库

Kubernetes Secret 默认只是经过 Base64 编码，并不等于天然加密。这里的 Secret 只约定键名，
具体值由集群密钥管理、External Secrets 或发布平台注入。仓库中的 Kustomize base 不包含任何
可用的数据库密码、Token 或签名密钥。

## 影响和边界

- `maxUnavailable: 0` 会在发布期间暂时增加一个 Pod，需要预留集群容量；
- 两个 API 副本会同时运行后台 Worker，任务必须继续依赖数据库租约、Inbox 和稳定幂等键防止重复处理；
- PDB 只约束自愿中断，不保证节点宕机或整个可用区故障时仍有副本；
- 探针只能反映进程和依赖 readiness，不能替代错误率、延迟、业务成功率和队列 Lag 监控；
- 本节没有加入 Ingress、Service Mesh、HPA、跨可用区拓扑和真实密钥管理，它们需要结合目标集群单独设计。

## 重新评估条件

- 接入真实集群后，增加服务器端 schema 校验、策略检查、镜像签名和漏洞扫描；
- 需要自动扩缩容时，根据性能基线、队列积压和 Provider 全局配额设计 HPA 或 Worker 独立部署；
- 需要生产金丝雀时，在 Deployment 基础上接入入口层权重、发布审批、自动回滚门槛和审计记录。
