# 第 1 课：从零设计内存版订单服务

> 当前仓库已预留大型项目的模块化目录，但暂时没有预写工程代码。你负责实现代码，目录和边界说明见 [`docs/architecture/project-structure.md`](docs/architecture/project-structure.md)。

## 目标

从空的 `internal/order/` 目录开始设计并实现订单服务。这一课练习业务建模、Go 错误语义、Context 和表格驱动测试，不涉及 HTTP、数据库和并发安全。

预计用时：60～90 分钟。

## 业务规则

调用 `CreateOrder(ctx, input)` 时：

1. 如果 Context 已取消，立即返回它的错误，不能修改库存。
2. 购买数量必须大于 0，否则返回 `ErrInvalidQuantity`。
3. 商品不存在时返回 `ErrProductNotFound`。
4. 库存不足时返回 `ErrInsufficientStock`，库存不能变化。
5. 创建成功后扣减库存并返回订单。
6. 每个成功创建的订单必须拥有非空且不重复的 ID。
7. 调用方必须能够使用 `errors.Is` 判断上述错误。

错误优先级按照上面的顺序处理。例如，一个已经取消的请求即使数量无效，也应该先返回 Context 错误。

## 你的任务

你需要自己创建下面两个文件：

```text
internal/order/
├── service.go
└── service_test.go
```

不要从其他项目复制完整实现。先根据业务规则设计 `Product`、`Order`、输入参数和服务对象，再实现创建订单流程。

开始前先运行：

```bash
go test ./...
```

由于目录目前没有 Go 源文件，`go test ./...` 会先验证模块结构。接下来由你创建代码和测试，再逐条实现业务规则。

完成后执行：

```bash
make fmt
make check
```

如果本机不提供 `make`，可以直接执行：

```bash
gofmt -w .
go test ./...
go test -race ./...
go vet ./...
```

## 思考题

先不用修改代码，但请把答案写到 `../notes/learning-log.md`：

1. 为什么错误定义为包级变量后，调用方可以用 `errors.Is` 判断？
2. 为什么失败路径必须保证库存不变？
3. 当前代码在两个请求并发创建订单时是否安全？如何证明？
4. 如果将来订单要写入数据库，事务边界可能放在哪里？

## 当前刻意保留的限制

- 数据只存在内存中。
- Service 还不支持并发调用。
- 没有 HTTP 接口。
- 没有引入 Repository 接口。
- 没有使用任何第三方依赖。

这些不是遗漏，而是后续课程要逐步解决的问题。
