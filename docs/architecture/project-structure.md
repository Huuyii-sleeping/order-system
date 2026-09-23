# 项目结构与模块边界

本项目采用模块化单体作为大型项目的起点：先在一个进程内明确模块边界、依赖方向和业务责任，再根据真实的性能与部署需求拆分服务。

当前目录只搭建结构，不预先生成业务代码。后续每个阶段由学习者根据业务需求填充实现。

## 目标结构

```text
order-system/
├── cmd/
│   └── api/                    # HTTP API 进程的启动入口
├── internal/
│   ├── app/                    # 应用组装、依赖注入、生命周期
│   ├── config/                 # 启动配置解析与校验
│   ├── domain/                 # 核心业务模型和业务规则
│   │   ├── user/
│   │   ├── catalog/            # 商品、价格
│   │   ├── inventory/          # 库存、预占、释放
│   │   ├── order/              # 订单与状态机
│   │   ├── payment/            # 支付意图、支付状态
│   │   └── notification/       # 通知业务规则
│   ├── application/            # 用例编排和事务边界
│   │   ├── user/
│   │   ├── catalog/
│   │   ├── inventory/
│   │   ├── order/
│   │   ├── payment/
│   │   └── notification/
│   ├── adapters/               # 外部依赖的具体实现
│   │   ├── postgres/
│   │   ├── redis/
│   │   ├── messaging/
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

## 当前迁移状态

- `internal/order/`：第一课的内存版订单服务目录，目前为空，由学习者从零实现。
- `internal/domain/order/`：未来的订单领域模块目录，目前为空。
- 其余目录：只保留结构占位，不包含预写业务代码。

后续迁移顺序建议是：

1. 先把当前订单业务接入 HTTP。
2. 再设计商品、库存和订单数据库模型。
3. 以一个完整的下单用例确定 `application` 和 `domain` 的边界。
4. 接入数据库实现，再补集成测试。
5. 遇到缓存、消息和可观测性需求时，逐步填充对应 adapter/platform 目录。

不要为了填满目录而创建接口或文件。目录是地图，不是任务清单。
