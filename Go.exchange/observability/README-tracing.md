# 推荐链路 OpenTelemetry Tracing

本地 API Tracing 默认关闭。关闭时不注册 HTTP Span Middleware，也跳过推荐链路阶段 Span；启用后，API 使用 OpenTelemetry BatchSpanProcessor 通过 OTLP gRPC 导出到 Grafana Tempo。Tempo 是可选 Compose 服务，不影响未启用 Tracing 的 API 启动。

## 启用与关闭

在 `D:\code\mf` 运行 Compose。下面的命令会启动 API、Prometheus、Grafana 和带 `observability` profile 的 Tempo：

```powershell
$env:TRACING_ENABLED = "true"
$env:TRACING_SAMPLE_RATIO = "1.0"
docker compose --profile observability up -d api prometheus grafana tempo
```

采样率 `1.0` 适合本地验证。默认 Service Name 为 `exchange-api`，采样率为 `0.05`，OTLP 地址为 `tempo:4317`，单批导出超时为 `3s`，进程退出刷新超时为 `5s`。可以分别通过 `TRACING_SERVICE_NAME`、`TRACING_OTLP_ENDPOINT`、`TRACING_EXPORT_TIMEOUT` 和 `TRACING_SHUTDOWN_TIMEOUT` 修改。

Compose 内部使用未加密的 `tempo:4317`，主机端口只绑定到 loopback。API 若直接在主机进程运行，请将 `TRACING_OTLP_ENDPOINT` 设为 `127.0.0.1:4317`。远程接收端可使用 `https://host:4317` 启用 TLS；不支持带认证信息、路径或查询参数的 Endpoint。

关闭 Tracing 并停止 Tempo：

```powershell
$env:TRACING_ENABLED = "false"
docker compose --profile observability stop tempo
docker compose up -d api
```

关闭状态不会创建 OTLP Exporter 或尝试连接 Tempo。无需数据库迁移；不启动 Tempo 也可以照常运行 API。Compose 数据保存在 `goexchange_tempo-data` 卷中。

## 后端镜像构建上下文

Tracing 编译包位于 `Go.exchange/observability/tracing`。`.dockerignore` 仅重新放行该目录中的 Go 源码，并继续排除 Grafana、Tempo、Prometheus、本地开发数据和本地凭据。CI 使用生产 Dockerfile 编译 API、Worker 和维护工具；可从 `D:\code\mf` 复现：

```powershell
docker build -f Go.exchange/Dockerfile Go.exchange
docker build -f Go.exchange/Dockerfile.prod -t goexchange-backend:ci Go.exchange
docker run --rm --entrypoint /bin/sh goexchange-backend:ci -c 'test -x /app/go-exchange-api && test -x /app/go-exchange-worker && test -x /app/go-exchange-kafka-dlq && test ! -e /app/observability && test ! -e /app/.secrets && test ! -e /src'
```

最后一条检查最终运行镜像保留必要二进制，并且没有把构建源码或 observability 配置复制进运行层。

## 在 Grafana 查看 Trace

1. 打开本地 Grafana：`http://127.0.0.1:3001`。
2. 进入 **Explore** 并选择自动配置的 **Tempo** 数据源。
3. 在 Search 模式按 `service.name=exchange-api` 查找请求，或粘贴 TraceID 查询单条 Trace。
4. 展开 HTTP 根 Span 和 `recommendation.*` 子 Span，对照错误状态、阶段、召回来源和候选数量。
5. 可使用以下 TraceQL 查找超过 1 秒的推荐 Trace：

```traceql
{ resource.service.name = "exchange-api" && trace:duration > 1s }
```

按用户推荐路由查找慢 Trace：

```traceql
{ resource.service.name = "exchange-api" && name = "GET /api/recommendations/posts" && trace:duration > 1s }
```

查找带错误 Span 的推荐 Trace：

```traceql
{ resource.service.name = "exchange-api" && status = error }
```

TraceID 是 OpenTelemetry 的链路标识；`recommendation.request_id` 是业务请求标识。Service 返回非空 Request ID 时，HTTP 根 Span 在成功或失败路径都会记录它；推荐服务 Span 也会记录它。两者不能互相替代。`request_id` 不进入 Prometheus 标签。

HTTP 根 Span 使用规范化路由模板命名，并只记录 `http.request.method`、`http.route`、`http.response.status_code` 和可用的 `recommendation.request_id`。健康检查与 `/metrics` 不创建 Span。HTTP 错误状态使用固定描述；Gin `ctx.Errors` 的原始错误不会进入 Span Status 或 Exception Event。请求和响应 Body、Query、Authorization、Cookie、客户端 IP、端口、User-Agent 都不会被记录。业务错误响应仍按现有 Handler 逻辑生成。

公网入口接受 W3C `traceparent` 用于保留 Trace ID 和父子关联，但把远端 sampled flag 和可由客户端选择的 Trace ID 当作不可信输入：远端父 Span 无论标记 sampled 与否，都由服务端按 `TRACING_SAMPLE_RATIO` 做独立随机采样。服务内的本地父 Span 继续使用 ParentBased 的父采样决定。当前没有单独的可信服务间 HTTP 入口，因此传入 API 的远端上下文一律执行本地比例；客户端不能只靠设置 sampled bit 或挑选 Trace ID 强制完整采集。

## 用 Metrics 发现慢请求

推荐接口额外记录毫秒级桶的 Histogram：`go_exchange_recommendation_http_duration_seconds`。该指标与 Tracing 开关无关，只包含低基数 `route` 和 `status` 标签。计时范围是 Metrics 中间件开始执行到下游超时中间件及 Handler 返回；它不等于单条 SQL 或 Redis 命令在服务端的执行时间。

例如，在 Prometheus 中查看近五分钟成功推荐请求的 P95：

```promql
histogram_quantile(
  0.95,
  sum by (le, route) (
    rate(go_exchange_recommendation_http_duration_seconds_bucket{status=~"2.."}[5m])
  )
)
```

排查接口从 3ms 升到 3s：

1. 用 Prometheus 的推荐接口 P95 确认延迟上升。
2. 在 Tempo 找一条慢 Trace，并与正常请求对比。
3. 查看 `recommendation.recall.semantic`、History、Profile、Hydrate、Rank、Select 和 Response Map 等阶段。
4. 如果慢在候选读取或补全，再结合 PostgreSQL 连接池、查询计划和 Redis 状态排查。
5. 以相同数据集、用户画像命中率和并发量重跑，比较修复前后延迟。

阶段 Span 只表示应用侧操作时间。例如候选读取 Span 可能还包含连接池等待、扫描和结果转换，不能直接视为 PostgreSQL SQL 执行时间；历史 Span 也可能包含客户端排队和网络等待，不能直接视为 Redis 服务端命令耗时。嵌套 Span 的 Duration 有重叠，不能相加来计算请求总耗时。头采样可能漏掉慢请求，因此没有 Trace 不代表请求没有发生。

## 性能对比方法

先运行可复现的 HTTP 和推荐 Span 微基准，区分旧式无 Tracing 路径、Disabled、新版 5% 和 100% 采样路径：

```powershell
Set-Location D:\code\mf\Go.exchange
go test ./router -run '^$' -bench '^BenchmarkHTTPTracing$' -benchmem -count=5
go test ./recommendation -run '^$' -bench '^BenchmarkRecommendationSpanOverhead$' -benchmem -count=5
```

这些基准只测 Gin 路由/Middleware 和推荐 Span helper，不包含推荐数据访问、Tempo 网络、数据库或 Redis。完整接口比较仍需使用下方相同数据集的 k6 负载测试。

仓库的 `loadtest/read-heavy.js` 会以固定脚本发起推荐及其他只读请求。先按 [`loadtest/README.md`](../loadtest/README.md) 创建相同的本地合成用户并预热服务，然后对每种配置使用相同的 k6 profile、数据和并发，分别运行 Tracing 关闭、采样率 `0.01`、`0.1` 和 `1.0` 四组。记录 k6 的 RPS、P50/P95/P99 和错误率，并在每组运行期间用 `docker stats` 记录 API 容器 CPU 与内存；保存每组配置、测试摘要和容器统计以便复查。不要将不同候选分布、画像命中率或数据库/Redis 负载的结果直接比较。

本地开发步骤：

```powershell
Set-Location D:\code\mf
$env:TRACING_ENABLED = "false"
docker compose up -d api
# 在另一终端按 loadtest/README.md 运行相同的 k6 profile，保存摘要。

$env:TRACING_ENABLED = "true"
$env:TRACING_SAMPLE_RATIO = "0.01" # 再依次使用 0.1 和 1.0
docker compose --profile observability up -d api tempo
# 重启 API 使环境变量生效，再运行同一 k6 profile 并保存摘要。
```

HTTP 和 helper 微基准不能替代完整接口验收。除非同时记录真实本地 PostgreSQL、Redis、Tempo、相同 k6 profile 和多轮运行结果，否则不要据此声称完整接口的 P95、CPU、内存、GC 或 RPS 回归情况。

## 故障与隐私边界

- OTLP 导出在有界批处理队列中异步进行；队列有最大容量，单批导出与关闭刷新都有超时。Tempo 不可用时可能丢弃 Span，但不应让每个业务请求同步等待导出。`observability/tracing` 的阻塞 Exporter 单元测试只覆盖 Span End 不等待测试 Exporter，不代替真实网络故障测试。
- Trace 只写入已审查的阶段属性、低基数来源/阶段值、候选数和业务 `request_id`；不写原始 URL 路径、客户端 IP/端口、User-Agent、用户 ID、Guest Session、帖子正文、Redis Key、向量、Authorization、SQL 参数或请求/响应 Body。
- 自定义 Gin Server Middleware 覆盖路由处理并使用路由模板作为名称；`/metrics`、`/healthz` 和 `/readyz` 不创建 HTTP Span。Middleware 只检查 `ctx.Errors` 是否非空来标记失败，不读取错误文本、不序列化该列表，也不修改它。
- 这是本地单进程 Tempo 配置，不是生产高可用、鉴权或长期保留方案。
- 不启动或停止 Tempo 不会修改业务数据库；若需要回滚配置，只需关闭 `TRACING_ENABLED` 并恢复本次代码/Compose 变更。

在 Docker Engine 可用的环境里复现 Tempo 中断检查：

```powershell
docker compose --profile observability up -d api tempo
# 按 loadtest/README.md 持续发送相同的推荐请求，并记录客户端延迟、API CPU/内存和日志。
docker compose --profile observability stop tempo
# 中断期间继续发送相同请求并记录同一组指标。
docker compose --profile observability up -d tempo
# Tempo 恢复后再次发送请求，在 Grafana Explore 检查恢复后生成的新 Trace。
```

Tempo 启动、OTLP→Tempo 接收、Grafana 查询、真实推荐 Trace 以及网络中断期间的 API 指标，必须分别记录实际运行证据；Compose 配置解析和 InMemoryExporter 测试不能替代这些检查。
