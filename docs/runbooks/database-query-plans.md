# 数据库查询计划与 N+1 排查

## 这节解决什么问题

“接口能返回正确结果”不等于“数据库访问可以随着数据量增长”。查询计划决定 PostgreSQL
是走索引还是扫描整张表；N+1 则常常发生在 Go 的循环里：先查出一页订单，再对每条订单
单独查询商品、支付或用户，SQL 次数从 1 变成 `1 + N`。

订单历史的目标查询形状是：

```sql
WHERE customer_id = $1
  AND (created_at, id) < ($2, $3)
ORDER BY created_at DESC, id DESC
LIMIT $4
```

对应索引是 `orders_customer_created_at_id_idx`。`customer_id` 先完成用户隔离，后两个字段
同时承担游标边界和稳定排序，因此不需要随着页码加深而扫描并丢弃大量 `OFFSET` 记录。

## 运行结构性回归

PostgreSQL 已启动并完成迁移后，执行：

```bash
make database-query-plan-check
```

测试会在独立 schema 中写入 5,000 条订单，运行 `ANALYZE`，然后执行
`EXPLAIN (FORMAT JSON, COSTS OFF)`。它要求计划包含 `orders_customer_created_at_id_idx`，
并且不包含 `Seq Scan`。没有 `TEST_DATABASE_URL` 时测试会跳过，而不是假装已经完成数据库验收。

完整适配器验收仍然使用：

```bash
make test-integration
```

## 如何阅读计划

- `Index Scan` / `Index Only Scan`：通常说明规划器可以沿目标索引定位并按索引顺序读取。
- `Bitmap Index Scan` + `Bitmap Heap Scan`：索引参与了定位，但还要批量回表；要结合返回行数和
  排序节点判断是否合适。
- `Seq Scan`：读取整张表。小表上可能是合理选择；在高基数列表查询中持续出现则需要检查
  索引、谓词选择性和统计信息。
- `Sort`：如果排序字段与索引顺序不匹配，数据库可能先取数据再排序；Keyset 查询应尽量让
  复合索引同时覆盖过滤和排序。

`EXPLAIN` 只生成计划，不执行 SQL。生产排障使用 `EXPLAIN (ANALYZE, BUFFERS)` 前要确认
查询是只读、参数是安全的，并控制返回范围；它会真实执行语句，写操作尤其危险。

## N+1 排查顺序

1. 先从 Trace 或结构化 SQL 日志确认一个 HTTP 请求实际发出了多少次同类查询，不要凭代码形状
   猜测。
2. 使用固定页大小做对比：如果返回 20 条订单时查询数接近 21，返回 40 条时接近 41，优先
   怀疑 N+1；连接池等待或慢查询本身不等同于 N+1。
3. 确认重复查询是否真的需要逐条隔离。如果不需要，优先增加批量端口，例如
   `GetProductsByIDs(ctx, ids)`，用一次 `WHERE id = ANY($1)` 或 Join 读取。
4. 保留跨用户隔离和结果顺序测试。批量查询不能因为优化而把其他用户的数据混入响应，也不能
   让数据库返回顺序覆盖业务定义的排序。
5. 优化后再次执行查询计划回归和 HTTP/数据库集成测试；不要用关闭 `enable_seqscan` 的方式
   掩盖生产计划问题。

## 常见失败处理

- 找不到目标索引：确认迁移 `000010_order_list_keyset_index.sql` 已应用，并检查当前 schema
  的索引名称。
- 计划出现 `Seq Scan`：先执行 `ANALYZE orders`，再查看客户数据分布和完整计划；小表或极低
  选择性下的顺序扫描可能是合理的。
- 计划出现目标索引但接口仍慢：继续看 `BUFFERS`、回表行数、排序、锁等待和连接池等待，
  不要把“使用索引”当成性能保证。
- 集成测试跳过：设置 `TEST_DATABASE_URL`，并确认 Docker PostgreSQL 已健康启动；跳过不等于
  验收通过。
