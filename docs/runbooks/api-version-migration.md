# API v1 迁移与下线手册

## 当前入口

规范入口：

```bash
curl -i http://127.0.0.1:8080/api/v1/products
```

响应包含 `API-Version: v1`，不包含 `Deprecation`。旧入口仍可用：

```bash
curl -i http://127.0.0.1:8080/products
```

旧入口会额外返回 RFC 9745 `Deprecation` 和：

```text
Link: </api/v1/products>; rel="successor-version"
```

## 迁移步骤

1. 从网关、客户端仓库和负责人清单确认所有调用方，不能只看最近流量。
2. 调用方把 base URL 改为 `/api/v1`，请求体、响应体和稳定错误码保持不变。
3. 登录调用方必须从 versioned login 重新取得 Refresh Cookie；其 Path 是 `/api/v1/auth`。
4. 观察新入口增长和旧入口下降：

   ```promql
   sum(rate(order_system_http_api_access_total{version="v1",entrypoint="legacy"}[5m]))
   ```

5. 在约定观察窗口内保持为零后，再核对低频定时任务、Webhook Provider、灾备脚本和回滚版本。
6. 负责人批准删除日期后才增加 `Sunset`，并在发布说明中给出替代入口。
7. 删除旧入口必须单独发布；不要和 v2 业务语义切换放在同一次发布中。

## 什么情况需要 v2

以下变化不能直接覆盖 v1：

- 删除或重命名响应字段；
- 改变字段类型、单位或业务含义；
- 给请求增加必填字段；
- 改变认证方式、幂等语义或稳定错误码含义。

新增 v2 时，先建立新的 Handler/DTO 和 OpenAPI 契约，再复用已有应用用例。数据库迁移仍采用
expand/contract，不能因为 HTTP 版本变化就维护两份业务数据。

## 验收

```bash
make contract-check
go test ./internal/transport/httpapi ./internal/platform/metrics
```

检查规范入口没有弃用 Header，旧入口有 successor Link，并确认 Prometheus 只使用
`version`、`entrypoint` 两个有界标签，不放 URL、用户 ID 或客户端名称。
