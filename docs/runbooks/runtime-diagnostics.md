# Go pprof 与运行时诊断手册

## 什么时候使用

先通过 SLO、Metrics、Trace 和 Log 判断问题范围，再选择 Profile：

| 现象 | 优先查看 |
| --- | --- |
| CPU 持续升高、请求变慢 | CPU Profile |
| RSS 或 Go heap 持续增长 | heap、allocs、GC 指标 |
| goroutine 数量持续增长 | goroutine Profile |
| 锁等待、并发吞吐下降 | mutex Profile |
| channel、网络或同步阻塞 | block Profile、goroutine |
| GC、调度、系统调用时序异常 | runtime trace |

pprof 不是常规健康检查。没有先确定异常时间段和症状就随意采样，通常只会得到一份“正常运行时”
的快照。

## 本机启用

诊断服务默认关闭。本机启动时显式开启：

```bash
DIAGNOSTICS_ENABLED=true \
DIAGNOSTICS_ADDR=127.0.0.1:6060 \
go run ./cmd/api
```

确认诊断端口可用：

```bash
curl -fsS http://127.0.0.1:6060/debug/pprof/
```

业务端口不应出现 pprof：

```bash
curl -o /dev/null -w '%{http_code}\n' http://127.0.0.1:8080/debug/pprof/
```

预期返回 `404`。配置成 `0.0.0.0:6060`、`:6060`、`localhost:6060` 或普通网卡地址时，应用
应在业务端口监听前拒绝启动。

## CPU Profile

CPU Profile 是一段时间内的采样，不是瞬时快照。应在问题或代表性负载正在发生时采集：

```bash
go tool pprof -http=127.0.0.1:0 \
  'http://127.0.0.1:6060/debug/pprof/profile?seconds=30'
```

常用视角：

- `top`：函数自身消耗的 CPU；
- `top -cum`：函数连同下游调用累计消耗的 CPU；
- `list <function>`：把采样映射到具体代码行；
- Flame Graph：从调用栈宽度观察主要 CPU 路径。

不要仅凭一次很短的采样优化代码；先确认采样覆盖真实故障窗口，并用相同负载复测。

## Heap 与分配

查看当前堆中保留对象：

```bash
go tool pprof -http=127.0.0.1:0 \
  http://127.0.0.1:6060/debug/pprof/heap
```

`heap` 更接近当前仍存活的对象，`allocs` 反映进程启动以来累计分配热点。累计分配高不一定表示
内存泄漏；真正的泄漏或错误保留需要结合多次 heap、GC 后趋势、RSS 和业务负载判断。

## Goroutine

快速查看带栈信息的文本快照：

```bash
curl -fsS 'http://127.0.0.1:6060/debug/pprof/goroutine?debug=1'
```

重点观察相同调用栈的 goroutine 是否随时间持续增加，以及它们阻塞在 channel、锁、网络读取还是
没有退出条件的后台循环。数量高不自动等于泄漏，Worker 池和连接库本来就可能长期保留 goroutine。

## Mutex 与 Block

这两类采样默认关闭。只在已经怀疑锁竞争或阻塞时临时开启，例如：

```bash
DIAGNOSTICS_ENABLED=true \
DIAGNOSTICS_MUTEX_PROFILE_FRACTION=5 \
DIAGNOSTICS_BLOCK_PROFILE_RATE=1000000 \
go run ./cmd/api
```

然后查看：

```bash
go tool pprof -http=127.0.0.1:0 http://127.0.0.1:6060/debug/pprof/mutex
go tool pprof -http=127.0.0.1:0 http://127.0.0.1:6060/debug/pprof/block
```

Mutex fraction 表示平均每多少次竞争事件采样一次；Block rate 以阻塞纳秒为采样目标。更激进的采样
通常能看到更多细节，也可能产生更高运行时开销。问题结束后应恢复为 0 并重启实例。

## Runtime Trace

Trace 记录 goroutine 调度、GC、系统调用和网络阻塞等时序。采集时间应保持短小：

```bash
profile_dir=$(mktemp -d)
curl -fsS -o "$profile_dir/trace.out" \
  'http://127.0.0.1:6060/debug/pprof/trace?seconds=5'
go tool trace "$profile_dir/trace.out"
```

分析完成后删除临时目录。不要把 Trace 或 Profile 放进仓库。

## Docker 模式

Compose 不发布诊断端口到宿主机。启用后从容器内部显式抓取：

```bash
DIAGNOSTICS_ENABLED=true docker compose --profile application up -d --build --wait api
profile_dir=$(mktemp -d)
docker compose exec -T api \
  wget -qO- http://127.0.0.1:6060/debug/pprof/heap > "$profile_dir/heap.pprof"
go tool pprof -http=127.0.0.1:0 "$profile_dir/heap.pprof"
```

不要为了方便给 Compose 增加 `6060:6060`，也不要把地址改成 `0.0.0.0`。

## 安全与排障

- Profile 可能包含函数名、调用栈、本机路径、启动参数和业务类型，按敏感诊断数据处理；
- 不在聊天、工单或公开仓库上传未经审查的 Profile；
- pprof 返回 404：确认访问的是独立诊断端口，并且 `DIAGNOSTICS_ENABLED=true`；
- 应用启动失败：检查地址是否为显式回环 IP、端口是否被占用；
- mutex/block 几乎没有数据：确认对应采样配置为正数，并在竞争实际发生时采集；
- Profile 与 SLO 异常时间不一致：重新在故障窗口采集，不能用正常快照解释历史问题；
- 关闭超时：长时间 CPU Profile 或 Trace 会占用在途请求；全局关闭预算耗尽后服务会强制终止它。
