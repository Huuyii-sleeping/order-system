# HTTP SLO 与错误预算运行手册

## 目标和口径

本项目用两个 28 天滚动目标衡量 HTTP 业务请求：

```text
Availability SLI = 非 5xx 业务请求 / 全部业务请求
Latency SLI      = 500ms 内完成的业务请求 / 全部业务请求
```

目标分别是 99.9% 和 99%。`/healthz`、`/readyz`、`/metrics` 不进入分母；4xx 当前视为
availability good event。以 100,000 次请求为例：

- availability 最多允许 100 次 5xx；
- latency 最多允许 1,000 次超过 500ms。

这些值是本地学习基线，不是对外 SLA。

## 核心查询

最近一小时的 SLI：

```promql
100 * (1 - order_system:slo_error_ratio:rate1h{slo="http-availability"})
100 * (1 - order_system:slo_error_ratio:rate1h{slo="http-latency"})
```

当前快窗口燃烧率：

```promql
order_system:slo_error_ratio:rate5m{slo="http-availability"} / (1 - 0.999)
order_system:slo_error_ratio:rate5m{slo="http-latency"} / (1 - 0.99)
```

燃烧率含义：

```text
0x     没有消耗错误预算
1x     按当前速度会在 28 天刚好耗尽预算
6x     约 4.7 天耗尽预算
14.4x  约 46.7 小时耗尽预算
```

Grafana 的 `Order System Overview` 同时展示 1h SLI、28d 剩余预算和 5m/1h 燃烧率。
Prometheus 本地保留 30 天指标；新启动或清空数据卷后，需要积累数据才能代表完整 28 天窗口。

## 告警含义

### Fast burn / critical

1h 和 5m 都超过 14.4x，并且最近 5m 业务流量超过 0.1 requests/s。它表示影响较大且仍在发生，
应立即确认用户影响并控制故障扩散。

### Slow burn / warning

6h 和 30m 都超过 6x，并且最近 30m 业务流量超过 0.05 requests/s。它表示退化较慢但持续存在，
若不处理会在数天内耗尽预算。

双窗口已经验证“长期存在 + 当前仍在发生”，因此这四条规则没有额外 `for`。其他组件告警仍可能
使用 `for` 过滤短暂抖动。

## 告警处置

1. 在 Grafana 确认是 availability、latency，还是两者同时燃烧，并查看开始时间和流量变化。
2. 检查 TargetDown。抓取失败或实例退出时，应用侧 SLI 可能停止产生，不能把“没有 SLO 数据”
   当作恢复。
3. 按路由和状态码查看请求率；确认是全部接口、单一路由还是某类状态集中异常。
4. 对 latency 告警同时查看 HTTP p95、数据库连接池等待、Kafka Lag、Provider 耗时和熔断状态。
5. 使用异常时间段的 Trace 找到慢或失败的阶段，再用 `trace_id` 关联结构化日志。
6. 如果新版本与燃烧开始时间一致，停止放量并按 rollout runbook 判断回滚；数据库不兼容时不要
   只回滚应用。
7. 故障恢复后继续观察短窗口回落。长窗口会因历史样本继续偏高，这是滚动窗口的正常行为。
8. 记录消耗的错误预算、用户影响、根因和改进项，而不是仅关闭告警。

## 没有告警不等于没有问题

- 流量低于门槛时，SLI 仍会计算，但燃烧率告警保持静默；关键低流量接口需要合成探测；
- DNS、TLS、负载均衡器在请求进入应用前失败时，应用 Counter 看不到这些请求；
- 业务返回 HTTP 200 但响应内容错误时，当前 availability SLI 仍会认为成功；应补充业务结果 SLI；
- 大量 429 当前不消耗 availability 预算，但可能表示容量或风控策略影响用户，需要单独观测；
- 本地数据不连续、指标重置或保留期不足时，不能用它证明正式 SLA。

## 修改目标时的检查

不要只改 Dashboard 上的数字。一次口径变更至少需要同步：

1. 与产品和运维确认用户行为、目标值、观察窗口与排除流量；
2. 更新 Recording Rule 和燃烧率分母；
3. 更新 fast/slow 告警阈值与流量门槛；
4. 更新 `promtool` 规则测试，覆盖触发和不触发场景；
5. 更新 Dashboard、ADR 和本运行手册；
6. 先在历史数据上回放，评估告警数量后再进入正式值班。
