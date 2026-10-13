# 项目结构与模块边界

本项目采用模块化单体作为大型项目的起点：先在一个进程内明确模块边界、依赖方向和业务责任，再根据真实的性能与部署需求拆分服务。

当前项目已经实现用户认证、商品查询、事务下单、可靠事件消费、支付任务、支付结果 Saga、
通知可靠投递，并提供内存和 PostgreSQL 两种存储适配器。
后续仍按照业务需求逐步扩展，不为了填满目录提前创建无用抽象。

## 目标结构

```text
order-system/
├── cmd/
│   └── api/                    # HTTP API 进程的启动入口
├── internal/
│   ├── app/                    # 应用组装、依赖注入、生命周期
│   ├── config/                 # 启动配置解析与校验
│   ├── domain/                 # 核心业务模型和业务规则
│   │   ├── identity/           # 用户和会话
│   │   ├── catalog/            # 商品、价格
│   │   ├── inventory/          # 库存、预占、释放
│   │   ├── order/              # 订单与状态机
│   │   ├── payment/            # 支付意图、支付状态
│   │   └── notification/       # 通知业务规则
│   ├── application/            # 用例编排和事务边界
│   │   ├── event/              # 版本化集成事件契约
│   │   ├── auth/               # 注册、登录和会话认证
│   │   ├── catalog/
│   │   ├── inventory/
│   │   ├── order/
│   │   ├── payment/
│   │   └── notification/
│   ├── adapters/               # 外部依赖的具体实现
│   │   ├── postgres/
│   │   ├── redis/
│   │   ├── messaging/
│   │   ├── paymentprovider/    # 第三方支付端口实现
│   │   ├── clock/
│   │   └── idgenerator/
│   ├── transport/              # 外部协议适配
│   │   ├── httpapi/
│   │   └── grpcapi/
│   └── platform/               # 横向基础能力
│       ├── logging/
│       ├── metrics/
│       ├── observability/
│       └── telemetry/
├── migrations/                 # 数据库结构变更
├── deployments/                # 本地、测试和生产部署材料
│   ├── local/
│   ├── staging/
│   └── production/
├── tests/                      # 跨模块测试
│   ├── integration/
│   ├── contract/
│   ├── e2e/
│   └── load/                    # 固定并发性能基线工具
└── docs/                       # 架构、决策和运行手册
    ├── architecture/
    ├── decisions/
    └── runbooks/
```

## 依赖方向

```text
transport → application → domain
    │             │
    └─────────────┴── 依赖抽象，由 adapters 提供具体实现

app 负责把 domain、application、adapters、transport 组装起来。
```

基本规则：

- `domain` 不读取环境变量，不连接数据库，不依赖 HTTP 类型。
- `application` 表达用户用例，负责协调多个领域模块和事务边界。
- `adapters` 处理 PostgreSQL、Redis、消息队列等外部系统。
- `transport` 只负责协议转换、参数解析、认证入口和响应映射。
- `cmd` 只负责启动进程，不承载业务逻辑。
- `internal` 保证这些实现不能被项目外部直接导入。

这些是目标边界，不要求第一天全部实现。只有当业务需求逼出新的责任时，才创建对应的实现文件。

## 当前实现状态

- `internal/domain/`：已经包含用户、会话、商品、库存和订单领域模型。
- `internal/application/`：已经包含注册登录、商品查询、事务下单、订单历史游标分页、可靠事件、支付任务、
  支付结果驱动的订单确认或库存补偿，以及通知 Inbox/Task/Provider 投递用例。
- `internal/application/event/`：保存生产者与消费者共同依赖的版本化集成事件契约。
- `internal/adapters/memory/`：提供默认的进程内存实现。
- `internal/adapters/postgres/`：提供连接池、订单、Outbox、Inbox、支付任务、通知任务和迁移实现。
- `internal/adapters/redis/`：提供商品详情 Cache Aside 和跨实例共享的认证固定窗口限流。
- `internal/adapters/messaging/`：提供进程内和 Kafka 两种消息适配器，负责 Record
  信封、生产确认、消费者组、有限重试、死信隔离和 offset 提交。
- `internal/adapters/paymentprovider/`：提供带稳定幂等键的模拟第三方支付适配器，以及
  不侵入业务 Worker 的超时、并发舱壁和熔断装饰器。
- `internal/application/outbox/`：提供 Outbox 消息契约、租约认领、重试和 Worker 生命周期。
- `internal/application/payment/`：提供 Inbox 消费幂等、支付查询、支付任务和有界并发 Worker。
- `internal/application/notification/`：提供通知事件 Inbox 幂等、通知查询、任务租约、Provider 超时、稳定幂等键、查询对账、Webhook 回调消费和重试 Worker。
- `internal/application/auth/`：提供注册、登录、凭证轮换，以及独立的过期认证凭证清理维护 Worker。
- `internal/transport/httpapi/`：提供健康检查、认证、商品和订单 HTTP API；订单列表只使用认证身份并限制分页参数，通知 Webhook 则使用独立的 HMAC 机器认证。
- `internal/platform/metrics/`：提供独立 Prometheus 注册表、Go Runtime、进程、HTTP 请求、
  Kafka 消费和 PostgreSQL 连接池指标；连接池状态在抓取时读取，不启动独立采集 goroutine。
- `internal/platform/telemetry/`：初始化 OpenTelemetry，并用 W3C Trace Context 串联 HTTP、
  Outbox、Kafka、Payment Task 和支付结果 Saga。
- `deployments/local/observability/`：提供 Prometheus 抓取与告警规则、规则测试和
  Tempo Trace 存储、Grafana 自动配置；观测后端不参与业务事务。
- `migrations/`：包含数据库约束、索引和学习用种子数据。
- `internal/app/`：根据配置选择存储适配器并完成依赖组装。
- `internal/app/background.go`：统一监管后台组件，并把停止领取与取消在途工作拆成两阶段。
- `tests/load/`：提供不依赖第三方压测工具的固定并发 HTTP 性能基线；它只用于本地和 CI 验收，不属于生产 API。
- `cmd/api`：编排信号、readiness 排水、HTTP Shutdown、后台任务等待和资源逆序释放。

后续会在部署等真实需求出现时继续填充目录。

不要为了填满目录而创建接口或文件。目录是地图，不是任务清单。
