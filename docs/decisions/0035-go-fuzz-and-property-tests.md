# 0035：用 Go Fuzz Test 探索传输层输入边界

## 背景

普通表驱动测试会验证我们主动想到的几组输入，例如 malformed JSON、未知字段和超大 body，
但 HTTP 输入、分页游标和第三方签名 Header 的组合空间非常大。边界代码最常见的问题不是
“正常输入返回错”，而是某个未预料的字节序列触发 panic、绕过资源限制或破坏授权边界。

Go 原生 `testing.F` 支持把随机输入持续交给测试函数，并保存发现问题的最小 corpus。它适合
补充当前已经存在的单元测试，不替代契约、集成和 E2E 测试。

## 决定

- 为共享 JSON 解码器增加 fuzz target，要求任意输入都只能得到成功或传输层错误，不能 panic，
  也不能绕过 `MaxBytesReader`。
- 为订单 Keyset Pagination 查询解析增加 fuzz target；成功解析时必须保留认证用户 ID，并且
  `limit` 始终位于 1 到 100 的配置范围内。
- 为 Webhook HMAC 验证增加 fuzz target；任意随机 Timestamp、Signature 和 Payload 都必须
  fail closed，不能把被破坏的 malformed Signature 当成有效签名。
- Fuzz 测试只放在传输层，使用虚构的本地密钥和用户 ID，不调用数据库、网络或真实 Provider。
- 日常 CI 运行 fuzz corpus 的种子用例；定期或专项排障时使用 `-fuzztime` 延长探索时间，并
  把发现的最小回归 corpus 保留在代码仓库中。

## 影响和边界

- Fuzz “通过”只能说明探索到的输入没有违反测试不变量，不能证明所有业务规则、数据库事务或
  下游服务都正确。
- 随机测试的覆盖率和价值依赖不变量设计；只断言“不 panic”仍需要搭配状态、权限和资源边界
  断言。
- 当前没有把自动生成的 fuzz cache 或大型 corpus 提交到仓库；如果 Go 发现回归输入，应只保留
  可读、最小且不含敏感数据的样本。

## 重新评估条件

- 增加文件上传、复杂游标、压缩请求或新的签名协议时，为新的解析边界添加独立 fuzz target。
- 发现线上输入触发 bug 时，先把脱敏后的最小请求固化为回归测试，再决定是否加入 fuzz corpus。
- 业务逻辑出现大量状态组合时，增加针对领域状态机的属性测试，而不是只扩展 HTTP 样例。
