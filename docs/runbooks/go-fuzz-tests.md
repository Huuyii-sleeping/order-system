# Go Fuzz Test 运行手册

## 当前覆盖

- `FuzzDecodeJSONBodyNeverPanics`：任意 JSON/字节输入不会让共享 Decoder panic，且超大 body 仍
  留在传输层错误边界内。
- `FuzzParseOrderListQueryKeepsPaginationBounds`：任意查询字符串不会让 Keyset Pagination
  解析器 panic；成功结果只能读取已认证用户，limit 不能越过 1–100。
- `FuzzWebhookVerificationNeverPanics`：任意签名 Header 和原始 Payload 都不能触发 panic；被刻意破坏的 Signature 必须 fail closed。

## 日常运行种子 corpus

普通测试会执行每个 fuzz target 的初始种子：

```bash
go test ./internal/transport/httpapi
go test -race ./internal/transport/httpapi
```

这适合 CI，耗时稳定，但不会长时间生成随机输入。

## 延长探索时间

在本地专项排查时运行一个 target：

```bash
go test ./internal/transport/httpapi \
  -fuzz=FuzzDecodeJSONBodyNeverPanics \
  -fuzztime=30s
```

也可以替换为 `FuzzParseOrderListQueryKeepsPaginationBounds` 或
`FuzzWebhookVerificationNeverPanics`。发现失败时，Go 会输出可复现的 seed；先确认输入不含
Token、Cookie、真实订单或第三方报文，再把最小样本转成普通回归测试或安全的 fuzz seed。

## 如何理解“通过”

Fuzz 测试通过表示：在本次探索范围内，代码没有违反测试不变量。它不能证明：

- 数据库事务一定正确；
- Provider 一定不会超时；
- HTTP 契约字段一定没有变化；
- 线上真实流量一定没有性能问题。

因此它要和 `make ci`、真实集成、E2E、性能基线和故障演练一起使用。

## 排障顺序

1. 先记录 Go 输出的最小 seed 和失败 target。
2. 在同一个包中用 `go test -run` 或直接复现失败 seed。
3. 判断问题属于 panic、资源上限、解析语义、授权不变量还是业务状态。
4. 增加最小回归测试，避免只依赖随机复现。
5. 重新运行定向测试、race 和 `make ci`。

不要提交 `testdata/fuzz` 中未经审查的真实输入，也不要把 fuzz cache 当成构建产物上传。
