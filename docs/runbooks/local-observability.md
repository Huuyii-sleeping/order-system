# 本地可观测性运行手册

## 目标和边界

本手册用于在本机验证以下链路：

```text
Order System /metrics -> Prometheus -> PromQL / alerts -> Grafana
```

Prometheus 和 Grafana 运行在 Docker 中，Order System 仍由宿主机上的 Go 命令启动。
配置仅供本地学习：端口只绑定到 `127.0.0.1`，Grafana 使用公开的开发密码，
没有配置 Alertmanager、长期存储、高可用或生产认证。

## 启动

需要完整 Kafka 业务指标时，先启动依赖：

```bash
make infra-up
```

在第一个终端启动应用：

```bash
make run-kafka
```

在第二个终端检查规则并启动观测栈：

```bash
make observability-up
```

`observability-up` 会先执行两类检查：

1. `promtool check config` 验证 Prometheus 配置和规则语法。
2. `promtool test rules` 使用构造的时间序列验证告警何时触发或保持静默。

随后访问：

- Prometheus Targets：<http://127.0.0.1:9090/targets>
- Prometheus Alerts：<http://127.0.0.1:9090/alerts>
- Grafana：<http://127.0.0.1:3000>

Grafana 本地账号和密码均为 `admin`。打开 `Order System` 文件夹中的
`Order System Overview` 仪表盘。

## 验收

先确认 Prometheus 已成功抓取应用：

```bash
curl -fsS 'http://127.0.0.1:9090/api/v1/query?query=up%7Bjob%3D%22order-system%22%7D' | jq
```

结果中的值应为 `1`。再检查规则已被加载：

```bash
curl -fsS http://127.0.0.1:9090/api/v1/rules | jq
```

Grafana 健康检查：

```bash
curl -fsS http://127.0.0.1:3000/api/health | jq
```

创建订单并等待消息链路完成后，可以在 Prometheus 查询：

```promql
sum by (route, status) (rate(order_system_http_requests_total[1m]))
order_system_kafka_consumer_lag_records
sum by (outcome) (rate(order_system_kafka_processing_attempts_total[5m]))
```

## 理解和模拟告警

告警表达式为真后不一定立即进入 `firing`：带有 `for` 的规则会先进入 `pending`，
条件连续满足指定时间才会触发。这能过滤一次抓取失败或短暂流量尖峰。

最安全的本地故障模拟是停止 API，但保持 Prometheus 运行。大约一次抓取后，
`OrderSystemTargetDown` 会进入 `pending`；连续一分钟抓取失败后进入 `firing`。
重新启动 API，下一次成功抓取后告警恢复。

不要为了演示其他告警而随意修改业务数据库。对应表达式的确定性行为已经由
`promtool test rules` 覆盖；需要调整阈值时，先修改规则测试表达预期，再修改规则。

## 排障顺序

1. `/metrics` 无法访问：先检查应用是否启动以及监听地址。
2. Prometheus Target 为 Down：检查 `host.docker.internal:8080` 是否能从容器访问。
3. Prometheus 有数据但 Grafana 无图：检查自动配置的数据源和 Dashboard 是否加载。
4. 查询有结果但告警未触发：检查表达式、`for` 持续时间和规则评估时间。
5. 告警已触发但无人收到通知：当前没有 Alertmanager，这是预期边界。

## 停止

只停止观测组件并保留数据卷：

```bash
make observability-down
```

停止项目的全部 Compose 服务：

```bash
make infra-down
```

两个命令都不会删除命名数据卷。只有明确不再需要本地数据时，才应单独执行
带卷删除选项的 Docker Compose 命令。
