# 数据库连接池运行手册

## 目标和边界

本手册用于配置和排查 PostgreSQL 模式下的 `database/sql` 连接池。连接池负责复用连接、限制
单实例并发占用并在池满时让调用方等待；它不代替业务限流、SQL 超时、数据库慢查询分析或数据库
全局连接上限。

内存存储模式不会创建数据库连接池，也不会导出本手册中的指标。

## 配置

默认配置如下：

```text
DATABASE_MAX_OPEN_CONNS=20
DATABASE_MAX_IDLE_CONNS=5
DATABASE_CONN_MAX_LIFETIME=30m
DATABASE_CONN_MAX_IDLE_TIME=5m
```

所有值必须大于零，且 `DATABASE_MAX_IDLE_CONNS` 不能大于
`DATABASE_MAX_OPEN_CONNS`。配置非法时应用在启动阶段直接失败，不会带着不确定的池行为继续运行。

`DATABASE_MAX_OPEN_CONNS` 是单个应用实例的上限。确定它时先收集：

1. 数据库允许的总连接数；
2. 迁移、管理、监控和其他服务需要预留的连接数；
3. 应用可能同时运行的最大实例数。

例如数据库最多允许 100 条连接，预留 20 条给其他用途，应用最多运行 5 个实例，则单实例上限
不应超过 `(100 - 20) / 5 = 16`。这只是安全上限，不代表必须配置为 16；最终值还要通过负载测试
和数据库 CPU、IO、锁等待情况确定。

## 本地检查

配置和采集器单元测试：

```bash
go test ./internal/adapters/postgres ./internal/config ./internal/platform/metrics ./internal/app
```

Prometheus 规则与告警行为测试：

```bash
make observability-check
```

启动 PostgreSQL 模式和观测栈后，确认指标存在：

```bash
curl -fsS http://127.0.0.1:8080/metrics | grep order_system_database_pool
```

## 指标解读

```promql
order_system_database_pool_connections
order_system_database_pool_max_open_connections
rate(order_system_database_pool_waits_total[5m])
rate(order_system_database_pool_wait_duration_seconds_total[5m])
increase(order_system_database_pool_closed_total[15m])
```

- `connections{state="open"}`：当前已打开连接总数；
- `connections{state="in_use"}`：当前被查询或事务占用的连接；
- `connections{state="idle"}`：池中可立即复用的空闲连接；
- `waits_total`：因为达到最大打开连接数而发生等待的累计次数；
- `wait_duration_seconds_total`：这些等待累计消耗的时间；
- `closed_total`：连接因超过空闲数量、空闲时长或总生命周期被池回收的累计次数。

累计 Counter 一直增加是正常行为，不能直接把总值当成当前压力。使用 `rate()` 判断单位时间内是否
仍在等待，使用 `increase()` 判断一个观察窗口内增加了多少。

## 饱和告警

`OrderSystemDatabasePoolSaturated` 同时要求：

1. 使用中连接数至少达到配置上限的 90%；
2. 最近五分钟等待次数仍在增长；
3. 两个条件连续满足五分钟。

只有高利用率而没有等待，说明池很忙但仍能及时周转；只有历史累计等待值非零，也不能证明当前
仍然拥堵。把利用率和等待速率组合起来，才更接近“连接池正在成为瓶颈”。

## 告警排查顺序

1. 查看 `in_use`、`idle`、等待速率和等待耗时速率，确认是持续饱和而非短暂尖峰；
2. 查看 HTTP P95/P99、Worker 积压和超时，确认受影响的入口；
3. 在数据库侧检查慢 SQL、锁等待、长事务、CPU、IO 和最大连接使用情况；
4. 检查最近是否增加应用实例、Worker 并发，或把实例级配置误当成集群级配置；
5. 先修复慢查询、缺失索引、长事务和异常并发，再评估是否调整池预算；
6. 调高连接上限前重新计算集群理论总连接数，并确认数据库仍有运维与故障切换余量。

不要看到告警就直接调大连接池。连接池内排队至少受应用 Context 控制；把上限调得超过数据库承载
能力，会把排队转移到数据库，并扩大锁竞争和故障影响面。

## 常见现象

### `in_use` 接近上限，但 `waits_total` 没有增长

当前请求能够在连接归还后及时取得连接，暂时不是池饱和故障。继续观察延迟和持续时间，不需要仅因
利用率高立即扩池。

### 等待速率持续增长，数据库资源利用率不高

可能是单实例池过小，也可能是少量长事务、锁等待或连接泄漏占住池。先检查事务边界和 SQL 耗时；
只有在数据库还有容量且工作负载测试证明有效时才调高上限。

### 等待增长且数据库 CPU、IO 或连接数已经很高

数据库本身接近容量。此时扩池通常会恶化问题，应降低入口或 Worker 并发、优化查询、缩短事务，
必要时扩展数据库容量或拆分负载。

### `closed_total{reason="max_lifetime"}` 快速增长

检查连接最大生命周期是否设置过短。过度轮换会增加握手和认证开销；设置过长则可能让老连接长期
存在。应结合数据库、网络和连接代理的回收策略调整，而不是追求该指标为零。
