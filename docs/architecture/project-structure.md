# 项目结构与模块边界

本项目采用模块化单体作为大型项目的起点：先在一个进程内明确模块边界、依赖方向和业务责任，再根据真实的性能与部署需求拆分服务。

当前项目已经实现用户认证、商品查询、事务下单、可靠事件消费和支付任务，
并提供内存和 PostgreSQL 两种存储适配器。
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
│   └── e2e/
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
- `internal/application/`：已经包含注册登录、商品查询、事务下单、可靠事件和支付任务用例。
- `internal/application/event/`：保存生产者与消费者共同依赖的版本化集成事件契约。
- `internal/adapters/memory/`：提供默认的进程内存实现。
- `internal/adapters/postgres/`：提供连接池、订单、Outbox、Inbox、支付任务和迁移实现。
- `internal/adapters/messaging/`：提供异步消息适配器，当前用进程内路由模拟 Broker 投递。
- `internal/adapters/paymentprovider/`：提供带稳定幂等键的模拟第三方支付适配器。
- `internal/application/outbox/`：提供 Outbox 消息契约、租约认领、重试和 Worker 生命周期。
- `internal/application/payment/`：提供 Inbox 消费幂等、支付任务和第三方调用 Worker。
- `internal/transport/httpapi/`：提供健康检查、认证、商品和订单 HTTP API，认证中间件将可信用户 ID 写入 Context。
- `internal/platform/metrics/`：提供独立 Prometheus 注册表、Go Runtime、进程和 HTTP 请求指标。
- `migrations/`：包含数据库约束、索引和学习用种子数据。
- `internal/app/`：根据配置选择存储适配器并完成依赖组装。

后续会在缓存、消息和可观测性等真实需求出现时继续填充目录。

不要为了填满目录而创建接口或文件。目录是地图，不是任务清单。
