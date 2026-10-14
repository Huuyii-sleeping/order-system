# 0033：使用独立回环端口提供 Go 运行时诊断

## 背景

Metrics、SLO、Trace 和 Log 能发现用户影响并把问题收敛到服务或调用阶段，但不能直接回答以下
Go 进程内部问题：

- 哪些函数持续消耗 CPU；
- 哪些对象占用堆内存，是否存在内存保留或泄漏；
- goroutine 是否持续增长、阻塞在哪里；
- mutex 和 channel 等同步操作是否造成竞争；
- GC、调度器、系统调用与 goroutine 在一段时间内如何交互。

Go 标准库 pprof 和 runtime trace 可以回答这些问题。但 pprof 会暴露函数名、调用栈、构建路径、
启动参数和运行时结构；CPU Profile 与 Trace 还会执行持续一段时间的采样。如果直接注册到公开业务
Router，攻击者可能读取内部信息或主动制造诊断开销。

## 决定

- 使用 `internal/platform/diagnostics` 显式注册标准库 pprof Handler，不导入并依赖全局
  `http.DefaultServeMux`；
- 诊断服务默认关闭，通过 `DIAGNOSTICS_ENABLED=true` 临时启用；
- 使用独立 HTTP Server 和独立地址，默认 `127.0.0.1:6060`，不进入业务 Router、中间件和指标；
- 只接受显式 IPv4/IPv6 回环 IP。空 Host、`0.0.0.0`、`::`、普通网卡 IP 和主机名均拒绝；
- 构造 Server 时先绑定 Listener，端口冲突或地址错误会在业务 HTTP Server 接收流量前失败；
- CPU、heap、allocs、goroutine 和 runtime trace 使用 Go 默认能力；mutex 和 block 采样默认是 0，
  只能通过非负配置显式开启；
- 诊断 Server 作为 BackgroundRuntime 任务运行。意外退出会通知主进程；关闭时 stop Context 停止
  接收新请求，work Context 在全局关闭预算耗尽时强制中断在途 Profile；
- 响应设置 `Cache-Control: no-store` 和 `X-Content-Type-Options: nosniff`，Profile 文件不得提交
  到仓库。

## 为什么不用业务端口

业务端口通常经过入口网关、负载均衡器或 Service 暴露。即使给 pprof 添加普通用户认证，也容易
因为路由或策略变化意外开放，而且采集权限不应该等同于业务用户权限。

独立回环端口形成更小的网络边界：本机开发直接访问；容器模式只能进入容器抓取，不发布 Host
Port。正式环境若要采集，应通过受审计的临时运维通道，而不是创建长期公开路由。

## 影响和边界

- 默认关闭意味着发生故障后不能从未开启的进程补采历史 Profile；Metrics、Trace 和 Log 仍用于
  发现问题，必要时在受控实例临时开启诊断；
- 回环监听是网络边界，不是用户级授权。同一主机或同一容器命名空间内的进程仍可能访问；
- CPU Profile、runtime trace、mutex 和 block 采样会带来不同程度开销，不能为了“多收数据”
  长期开启高采样率；
- goroutine 文本和 Profile 仍可能包含路径、函数名或业务类型，必须按敏感诊断数据控制保存、
  分享和删除；
- pprof 显示资源消耗和调用栈，不自动证明业务根因。结论仍需要和 SLO 时间段、请求 Trace、日志、
  数据库与依赖指标交叉验证；
- 当前没有远程 Profile 存储、自动持续剖析或管理 API，也不会运行时动态改变采样配置。

## 重新评估条件

- 多实例生产环境需要持续剖析时，评估带认证、加密、采样控制和保留策略的 Continuous Profiling；
- 需要远程临时开启时，使用受认证且有审计记录的管理平面，不把开关放进公开 HTTP API；
- Profile 开销影响延迟 SLO 时，下调采样、缩短采集时间或只在隔离实例采集；
- 部署平台能提供可靠的临时端口转发后，仍保持回环绑定并记录谁在何时采集了什么 Profile。
