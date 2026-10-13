# Kubernetes 部署运行手册

## 清单离线检查

本地只渲染 Kustomize，不需要 Kubernetes 集群：

```bash
make kubernetes-check
kubectl kustomize deployments/kubernetes/base
```

成功只表示清单可以被 Kustomize 解析，不表示镜像、Secret、数据库、Redis、Kafka 或集群策略
已经可用。

## 准备运行时 Secret

不要把下面的值写入 Git。应在密钥管理系统或受保护的发布环境中设置变量后创建 Secret：

```bash
kubectl create namespace order-system --dry-run=client -o yaml | kubectl apply -f -

kubectl -n order-system create secret generic order-system-runtime \
  --from-literal=DATABASE_URL="$DATABASE_URL" \
  --from-literal=REDIS_URL="$REDIS_URL" \
  --from-literal=NOTIFICATION_WEBHOOK_SECRET="$NOTIFICATION_WEBHOOK_SECRET" \
  --dry-run=client -o yaml | kubectl apply -f -
```

`NOTIFICATION_WEBHOOK_SECRET` 应该使用随机高熵值；如果不启用 Webhook，也要根据部署策略
决定是否移除对应清单引用，而不是提交一个公开占位密钥。

## 发布前检查

1. 镜像已经推送并固定为不可变 tag 或 digest，替换
   `deployments/kubernetes/base/deployment.yaml` 中的占位镜像。
2. 目标集群已提供 PostgreSQL、Redis 和 Kafka 协议 Broker 的 Service DNS。
3. 迁移作为独立发布步骤成功完成，并且旧应用仍能兼容当前结构。
4. Secret 已存在，且没有把真实值写入命令历史、日志或 Git。
5. 已根据性能基线调整 resources；本仓库的 `100m/128Mi` 和 `1 CPU/512Mi` 只是学习基线。

## 发布与回滚

```bash
kubectl apply -k deployments/kubernetes/base
kubectl -n order-system rollout status deployment/order-system --timeout=10m
kubectl -n order-system get pods -l app.kubernetes.io/name=order-system
```

回滚应用镜像时：

```bash
kubectl -n order-system rollout history deployment/order-system
kubectl -n order-system rollout undo deployment/order-system
kubectl -n order-system rollout status deployment/order-system --timeout=10m
```

不要把 `rollout undo` 和数据库 `down migration` 视为同一件事。应用回滚前先确认旧镜像
能够读取已经存在的数据；数据库损坏时，进入新库恢复、数据对账和连接切换流程。

## 故障排查顺序

```text
Deployment 是否产生新 Pod
  → startupProbe 是否通过
  → readiness 是否通过且 Service 有 Endpoints
  → HTTP 错误率、P95/P99、业务成功率是否异常
  → PostgreSQL/Redis/Broker 是否可达
  → Worker、Consumer Lag、Provider 舱壁是否异常
  → 暂停放量或回滚应用镜像
```

如果 `/healthz` 是 200 但 `/readyz` 是 503，不应强行把 Pod 加入流量；先检查依赖和正在执行的
排水流程。若 Pod 被频繁重启，区分 liveness 失败、启动失败、资源 OOM 和节点驱逐，不要只看
HTTP 接口返回值。

## 本节没有验证的内容

- 没有连接真实 Kubernetes API Server；
- 没有验证真实 Ingress、Service Mesh、HPA、Pod 反亲和和跨可用区调度；
- 没有把 PostgreSQL、Redis、Kafka 当作可由这个 API 清单自动创建的生产组件；
- 没有把本地清单验收说成生产灰度或灾备验收。
